package handler

import (
	"errors"
	"net/http"

	"ansmeee-ai-agent/internal/agent"
	"ansmeee-ai-agent/internal/middleware"
	"ansmeee-ai-agent/internal/models"
	"ansmeee-ai-agent/internal/tracing"
	"ansmeee-ai-agent/pkg/logger"
	"ansmeee-ai-agent/pkg/response"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AgentHandler handles agent CRUD requests.
type AgentHandler struct {
	store *agent.AgentStore
}

// NewAgentHandler creates a new agent handler.
func NewAgentHandler(store *agent.AgentStore) *AgentHandler {
	return &AgentHandler{store: store}
}

type agentRequest struct {
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Prompt        string   `json:"prompt"`
	Tools         []string `json:"tools"`
	Model         string   `json:"model"`
	Temperature   *float64 `json:"temperature"`
	MaxTokens     int      `json:"max_tokens"`
	TopP          *float64 `json:"top_p"`
	MaxIterations int8     `json:"max_iterations"`
	Status        *int8    `json:"status"`
}

func (h *AgentHandler) userID(c *gin.Context) int64 {
	return c.GetInt64(middleware.CtxUserID)
}

// List returns agents for the current user.
func (h *AgentHandler) List(c *gin.Context) {
	ctx := c.Request.Context()
	uid := h.userID(c)
	if err := h.store.EnsureDefault(ctx, uid); err != nil {
		logger.L().Warn("ensure default agent failed", append(tracing.ErrFields(ctx, err), zap.Int64("user_id", uid))...)
	}
	agents, err := h.store.List(ctx, uid)
	if err != nil {
		logger.L().Error("list agents handler failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, err.Error())
		return
	}
	if agents == nil {
		agents = []*models.Agent{}
	}
	response.OK(c, gin.H{"agents": agents})
}

// Get returns a single agent.
func (h *AgentHandler) Get(c *gin.Context) {
	ctx := c.Request.Context()
	a, err := h.store.Get(ctx, c.Param("id"), h.userID(c))
	if err != nil {
		if errors.Is(err, agent.ErrAgentNotFound) {
			response.Fail(c, http.StatusNotFound, response.CodeNotFound, "agent not found")
		} else {
			logger.L().Error("get agent handler failed", tracing.ErrFields(ctx, err)...)
			response.InternalError(c, err.Error())
		}
		return
	}
	response.OK(c, a)
}

// Create adds a new agent.
func (h *AgentHandler) Create(c *gin.Context) {
	ctx := c.Request.Context()
	var req agentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}
	if req.Title == "" {
		response.BadRequest(c, "title is required")
		return
	}
	if req.Prompt == "" {
		response.BadRequest(c, "prompt is required")
		return
	}
	a, err := h.store.Create(ctx, h.userID(c), req.Title, req.Description, req.Prompt,
		req.Tools, req.Model, req.Temperature, req.MaxTokens, req.TopP, req.MaxIterations)
	if err != nil {
		logger.L().Error("create agent handler failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, err.Error())
		return
	}
	response.OK(c, a)
}

// Update modifies an existing agent.
func (h *AgentHandler) Update(c *gin.Context) {
	ctx := c.Request.Context()
	var raw map[string]any
	if err := c.ShouldBindJSON(&raw); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}
	a, err := h.store.Update(ctx, c.Param("id"), h.userID(c), raw)
	if err != nil {
		logger.L().Error("update agent handler failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, err.Error())
		return
	}
	response.OK(c, a)
}

// Delete removes an agent.
func (h *AgentHandler) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.store.Delete(ctx, c.Param("id"), h.userID(c)); err != nil {
		logger.L().Error("delete agent handler failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, err.Error())
		return
	}
	response.OK(c, gin.H{"deleted": true})
}
