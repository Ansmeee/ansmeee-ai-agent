package router

import (
	"ansmeee-ai-agent/internal/agent"
	"ansmeee-ai-agent/internal/config"
	"ansmeee-ai-agent/internal/handler"
	"ansmeee-ai-agent/internal/llm"
	"ansmeee-ai-agent/internal/memory"
	"ansmeee-ai-agent/internal/middleware"
	"ansmeee-ai-agent/internal/tool"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Setup configures all routes and middleware on the Gin engine.
func Setup(cfg *config.Config, logger *zap.Logger, mem memory.SessionStore, engine *agent.Engine, registry *tool.Registry, agentStore *agent.AgentStore, modelConfigStore *llm.ModelConfigStore, db *gorm.DB) *gin.Engine {
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

	// Serve frontend.
	r.StaticFile("/", "./web/agents.html")
	r.StaticFile("/chat", "./web/index.html")
	r.StaticFile("/agents", "./web/agents.html")
	r.StaticFile("/agent", "./web/agent.html")
	r.StaticFile("/user", "./web/user.html")
	r.StaticFile("/login", "./web/login.html")

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
	}

	return r
}
