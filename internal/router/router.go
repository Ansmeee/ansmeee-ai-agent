package router

import (
	"ansmeee-ai-agent/internal/agent"
	"ansmeee-ai-agent/internal/config"
	"ansmeee-ai-agent/internal/handler"
	"ansmeee-ai-agent/internal/kb"
	"ansmeee-ai-agent/internal/llm"
	"ansmeee-ai-agent/internal/memory"
	"ansmeee-ai-agent/internal/middleware"
	"ansmeee-ai-agent/internal/tool"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"strings"
)

// Setup configures all routes and middleware on the Gin engine.
func Setup(cfg *config.Config, logger *zap.Logger, mem memory.SessionStore, engine *agent.Engine, registry *tool.Registry, agentStore *agent.AgentStore, modelConfigStore *llm.ModelConfigStore, kbMgr *kb.KBManager, db *gorm.DB) *gin.Engine {
	gin.SetMode(cfg.Server.Mode)

	r := gin.New()

	// Global middleware.
	r.Use(middleware.Recovery(logger))
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(logger))
	r.Use(middleware.CORS(cfg.Server.CORSOrigin))

	// Handlers.
	chatHandler := handler.NewChatHandler(mem, agentStore)
	streamHandler := handler.NewStreamHandler(engine, mem, agentStore, modelConfigStore)
	toolHandler := handler.NewToolHandler(registry)
	agentHandler := handler.NewAgentHandler(agentStore)
	modelConfigHandler := handler.NewModelConfigHandler(modelConfigStore)
	authHandler := handler.NewAuthHandler(db, cfg.Server.JWTSecret)
	var kbHandler *handler.KBHandler
	if kbMgr != nil {
		kbHandler = handler.NewKBHandler(kbMgr, agentStore)
	}

	// Serve frontend SPA (Vue 3).
	r.Static("/web", "./web")
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(404, gin.H{"code": 404, "message": "not found"})
			return
		}
		c.File("./web/index.html")
	})

	// Auth routes (public).
	r.POST("/api/v1/auth/register", authHandler.Register)
	r.POST("/api/v1/auth/login", authHandler.Login)

	// API routes (JWT protected).
	v1 := r.Group("/api/v1")
	v1.Use(middleware.JWTAuth(cfg.Server.JWTSecret))
	{
		v1.GET("/auth/me", authHandler.Me)
		v1.POST("/chat/completion", streamHandler.Handle)
		v1.GET("/chat/:sessionId", chatHandler.History)
		v1.DELETE("/chat/:sessionId", chatHandler.Delete)
		v1.POST("/sessions", chatHandler.CreateSession)
		v1.GET("/sessions", chatHandler.ListSessions)
		v1.GET("/tools", toolHandler.Handle)
		v1.GET("/tools/:name/schema", toolHandler.Schema)
		v1.GET("/health", handler.HealthCheck)
		v1.GET("/user/model", modelConfigHandler.Get)
		v1.POST("/user/model", modelConfigHandler.Save)

		v1.GET("/agents", agentHandler.List)
		v1.GET("/agents/:id", agentHandler.Get)
		v1.POST("/agents", agentHandler.Create)
		v1.PUT("/agents/:id", agentHandler.Update)
		v1.DELETE("/agents/:id", agentHandler.Delete)

		// Agent knowledge base (per-agent isolation).
		if kbHandler != nil {
			v1.GET("/agents/:id/kb", kbHandler.GetKB)
			v1.PUT("/agents/:id/kb", kbHandler.UpsertKB)
			v1.GET("/agents/:id/kb/docs", kbHandler.ListDocs)
			v1.POST("/agents/:id/kb/docs", kbHandler.AddDoc)
			v1.GET("/agents/:id/kb/docs/:docId", kbHandler.GetDoc)
			v1.DELETE("/agents/:id/kb/docs/:docId", kbHandler.DeleteDoc)
			v1.POST("/agents/:id/kb/docs/:docId/reindex", kbHandler.ReindexDoc)
			v1.POST("/agents/:id/kb/query", kbHandler.Query)
		}
	}

	return r
}
