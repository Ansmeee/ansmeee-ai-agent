package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ansmeee-ai-agent/internal/agent"
	"ansmeee-ai-agent/internal/config"
	"ansmeee-ai-agent/internal/llm"
	"ansmeee-ai-agent/internal/memory"
	"ansmeee-ai-agent/internal/router"
	"ansmeee-ai-agent/internal/tool"
	"ansmeee-ai-agent/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

func main() {
	configPath := flag.String("config", "configs/config.yaml", "path to config file")
	flag.Parse()

	// Load configuration.
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// Initialize logger.
	if err := logger.Init(logger.Config{
		Level:      cfg.Log.Level,
		Format:     cfg.Log.Format,
		Output:     cfg.Log.Output,
		Filename:   cfg.Log.Filename,
		MaxSize:    cfg.Log.MaxSize,
		MaxBackups: cfg.Log.MaxBackups,
		MaxAge:     cfg.Log.MaxAge,
		Compress:   cfg.Log.Compress,
	}); err != nil {
		log.Fatalf("failed to init logger: %v", err)
	}
	appLogger := logger.L()
	defer logger.Sync()

	logger.Info("starting server",
		zap.Int("port", cfg.Server.Port),
		zap.String("mode", cfg.Server.Mode),
	)

	// Initialize GORM DB with master/slave.
	gormDB, err := openGORM(context.Background(), cfg)
	if err != nil {
		logger.Fatal("failed to open database", zap.Error(err))
	}
	logger.Info("database connected", zap.String("master", cfg.MySQL.Master.Host))

	// Initialize agent store.
	agentStore, err := agent.NewAgentStoreWithDB(gormDB)
	if err != nil {
		logger.Fatal("failed to init agent store", zap.Error(err))
	}
	logger.Info("agent store initialized")

	// Initialize session store (MySQL or fallback to memory/redis).
	sessionStore, err := initSessionStore(context.Background(), cfg, gormDB)
	if err != nil {
		logger.Fatal("failed to init session store", zap.Error(err))
	}
	defer sessionStore.Close()

	// Initialize LLM provider.
	llmProvider, err := llm.New(&cfg.LLM)
	if err != nil {
		logger.Fatal("failed to init LLM provider", zap.Error(err))
	}

	// Initialize tool registry.
	reg := tool.NewRegistry()
	reg.Register(&tool.Calculator{})
	reg.Register(&tool.DateTime{})
	reg.Register(&tool.WebSearch{})
	reg.Register(&tool.Weather{})
	logger.Info("tools registered", zap.Int("count", len(reg.List())))

	// Initialize memory manager (L1 task state + L2 long-term fact memory).
	memoryCtx, memoryCancel := context.WithCancel(context.Background())
	defer memoryCancel()
	var memMgr *memory.MemoryManager
	if cfg.Memory.LongTerm.Enabled {
		factStore, ferr := memory.NewFactStore(gormDB, cfg.Memory.LongTerm.Scoring, cfg.Memory.Evolution.DecayFactor)
		if ferr != nil {
			logger.Warn("fact store init failed, long-term memory disabled", zap.Error(ferr))
		} else {
			// In-memory task backend (redis backend deferred).
			taskStore := memory.NewTaskStore(&cfg.Memory, nil)
			lt := cfg.Memory.LongTerm

			// Extractor: deterministic always; layer the LLM extractor when enabled.
			var extractor memory.Extractor = memory.NewDeterministicExtractor()
			if lt.LLMExtract && lt.ExtractionModel != "" {
				completer := memory.NewProviderCompleter(llmProvider, lt.ExtractionModel)
				extractor = memory.NewCompositeExtractor(
					memory.NewDeterministicExtractor(),
					memory.NewLLMExtractor(completer, 0),
				)
				logger.Info("llm extractor enabled", zap.String("model", lt.ExtractionModel))
			}

			opts := []memory.ManagerOption{memory.WithSummaryStore(factStore)}

			// Semantic vector channel (embedder + store), pluggable backend.
			if lt.Vector.Enabled {
				var embedder memory.Embedder
				if em, eerr := memory.NewOpenAIEmbedder(&cfg.LLM, lt.Vector.EmbeddingModel); eerr == nil {
					embedder = em
				} else {
					logger.Warn("openai embedder init failed, using hash embedder", zap.Error(eerr))
					embedder = memory.NewHashEmbedder(lt.Vector.EmbeddingDim)
				}
				opts = append(opts,
					memory.WithEmbedder(embedder),
					memory.WithVectorStore(memory.NewVectorStore(lt.Vector)),
				)
				logger.Info("semantic vector channel enabled", zap.String("backend", lt.Vector.Backend))
			}

			// Idle-session summarizer reuses the extraction model.
			if lt.ExtractionModel != "" {
				completer := memory.NewProviderCompleter(llmProvider, lt.ExtractionModel)
				if s := memory.NewSummarizer(completer); s != nil {
					opts = append(opts, memory.WithSummarizer(s))
				}
			}

			memMgr = memory.NewMemoryManager(
				sessionStore, taskStore, factStore,
				memory.NewQueryRouter(), extractor,
				lt, opts...,
			)
			go memory.NewIdleScanner(gormDB, memMgr, &cfg.Memory).Start(memoryCtx)
			logger.Info("memory manager initialized (fact + task + vector + summary)")
		}
	}

	// Initialize agent engine.
	cb := agent.NewCallback(appLogger)
	engine := agent.New(llmProvider, reg, sessionStore, cb,
		agent.WithMaxIter(cfg.Agent.MaxIterations),
		agent.WithToolTimeout(cfg.Agent.ToolTimeout),
		agent.WithMaxOutputLength(cfg.Agent.MaxOutputLength),
		agent.WithParallelToolCalls(cfg.Agent.ParallelToolCalls),
		agent.WithMaxContextMessages(cfg.Agent.MaxContextMessages),
		agent.WithMemoryManager(memMgr),
	)
	logger.Info("agent engine initialized")

	// Initialize model config store.
	modelConfigStore := llm.NewModelConfigStore(gormDB)
	logger.Info("model config store initialized")

	// Setup router and start server.
	r := router.Setup(cfg, appLogger, sessionStore, engine, reg, agentStore, modelConfigStore, gormDB)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	go func() {
		logger.Info("listening", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server error", zap.Error(err))
		}
	}()

	// Graceful shutdown.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Info("shutting down", zap.String("signal", sig.String()))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("server shutdown error", zap.Error(err))
	}
	logger.Info("server stopped")
}

func openGORM(_ context.Context, cfg *config.Config) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(cfg.MySQL.Master.DSN()), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open master: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get underlying sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.MySQL.Master.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MySQL.Master.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.MySQL.Master.ConnMaxLifetime)

	slaveDSN := cfg.MySQL.Slave.DSN()
	if slaveDSN != "" && slaveDSN != cfg.MySQL.Master.DSN() {
		if err := db.Use(dbresolver.Register(dbresolver.Config{
			Sources:  []gorm.Dialector{mysql.Open(cfg.MySQL.Master.DSN())},
			Replicas: []gorm.Dialector{mysql.Open(slaveDSN)},
			Policy:   dbresolver.RandomPolicy{},
		})); err != nil {
			return nil, fmt.Errorf("register dbresolver: %w", err)
		}
	}
	return db, nil
}

func initSessionStore(_ context.Context, cfg *config.Config, gormDB *gorm.DB) (memory.SessionStore, error) {
	switch cfg.Memory.Type {
	case "mysql":
		logger.Info("using MySQL session store")
		return memory.NewMySQLStore(gormDB)
	case "redis":
		logger.Info("using Redis memory backend", zap.String("addr", cfg.Redis.Addr))
		store, err := memory.NewRedis(&cfg.Redis, &cfg.Memory)
		if err != nil {
			logger.Warn("Redis unavailable, falling back to in-memory", zap.Error(err))
			return memory.NewInMemory(&cfg.Memory), nil
		}
		return store, nil
	default:
		logger.Info("using in-memory session store")
		return memory.NewInMemory(&cfg.Memory), nil
	}
}
