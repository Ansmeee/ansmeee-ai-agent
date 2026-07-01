package handler

import (
	"net/http"

	"ansmeee-ai-agent/internal/agent"
	"ansmeee-ai-agent/internal/memory"
	"ansmeee-ai-agent/internal/middleware"
	"ansmeee-ai-agent/internal/tracing"
	"ansmeee-ai-agent/pkg/logger"
	"ansmeee-ai-agent/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ChatHandler handles session management.
type ChatHandler struct {
	mem        memory.SessionStore
	agentStore *agent.AgentStore
}

// NewChatHandler creates a new session handler.
func NewChatHandler(mem memory.SessionStore, agentStore *agent.AgentStore) *ChatHandler {
	return &ChatHandler{mem: mem, agentStore: agentStore}
}

func (h *ChatHandler) userID(c *gin.Context) int64 {
	return c.GetInt64(middleware.CtxUserID)
}

// ChatRequest is the request body for chat requests.
type ChatRequest struct {
	SessionID string                 `json:"session_id"`
	Message   string                 `json:"message" binding:"required"`
	AgentID   string                 `json:"agent_id"`
	Metadata  map[string]interface{} `json:"metadata"`
}

// History returns the message history for a session.
func (h *ChatHandler) History(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := c.Param("sessionId")
	if sessionID == "" {
		response.BadRequest(c, "sessionId is required")
		return
	}

	exists, err := h.mem.Exists(ctx, sessionID)
	if err != nil {
		logger.L().Error("check session existence failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, err.Error())
		return
	}
	if !exists {
		response.Fail(c, http.StatusNotFound, response.CodeNotFound, "session not found")
		return
	}

	messages, err := h.mem.History(ctx, sessionID)
	if err != nil {
		logger.L().Error("get session history failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, err.Error())
		return
	}

	response.OK(c, gin.H{
		"session_id": sessionID,
		"messages":   messages,
	})
}

// CreateSession creates a new chat session.
func (h *ChatHandler) CreateSession(c *gin.Context) {
	ctx := c.Request.Context()
	var req struct {
		AgentID string `json:"agent_id"`
	}
	_ = c.ShouldBindJSON(&req)

	sessionID := uuid.New().String()
	if req.AgentID != "" {
		if err := h.mem.SetAgent(ctx, sessionID, req.AgentID, h.userID(c)); err != nil {
			logger.L().Warn("set agent for session failed", tracing.ErrFields(ctx, err)...)
		}
	}
	response.OK(c, gin.H{"session_id": sessionID})
}

// ListSessions returns active sessions, optionally filtered by agent_id query param.
func (h *ChatHandler) ListSessions(c *gin.Context) {
	ctx := c.Request.Context()
	agentID := c.Query("agent_id")
	sessions, err := h.mem.ListSessions(ctx, h.userID(c), agentID)
	if err != nil {
		logger.L().Error("list sessions failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, err.Error())
		return
	}
	if sessions == nil {
		sessions = []memory.SessionInfo{}
	}
	response.OK(c, gin.H{"sessions": sessions})
}

// Delete removes a session and its messages.
func (h *ChatHandler) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := c.Param("sessionId")
	if sessionID == "" {
		response.BadRequest(c, "sessionId is required")
		return
	}

	if err := h.mem.Delete(ctx, sessionID); err != nil {
		logger.L().Error("delete session failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, err.Error())
		return
	}

	response.OK(c, gin.H{"session_id": sessionID, "deleted": true})
}
