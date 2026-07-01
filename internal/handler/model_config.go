package handler

import (
	"ansmeee-ai-agent/internal/llm"
	"ansmeee-ai-agent/internal/middleware"
	"ansmeee-ai-agent/internal/models"
	"ansmeee-ai-agent/internal/tracing"
	"ansmeee-ai-agent/pkg/logger"
	"ansmeee-ai-agent/pkg/response"

	"github.com/gin-gonic/gin"
)

// ModelConfigHandler handles user model config API.
type ModelConfigHandler struct {
	store *llm.ModelConfigStore
}

// NewModelConfigHandler creates a new model config handler.
func NewModelConfigHandler(store *llm.ModelConfigStore) *ModelConfigHandler {
	return &ModelConfigHandler{store: store}
}

// Get returns the model configs for the current user.
func (h *ModelConfigHandler) Get(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetInt64(middleware.CtxUserID)
	cfgs, err := h.store.GetByUser(ctx, userID)
	if err != nil {
		logger.L().Error("get model configs failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, err.Error())
		return
	}

	result := gin.H{
		"chat":      nil,
		"embedding": nil,
	}
	for _, cfg := range cfgs {
		masked := *cfg
		masked.Token = maskToken(masked.Token)
		switch cfg.ModelType {
		case models.ModelTypeChat:
			result["chat"] = &masked
		case models.ModelTypeEmbedding:
			result["embedding"] = &masked
		}
	}
	response.OK(c, result)
}

func maskToken(token string) string {
	if len(token) < 10 {
		return "****"
	}
	return token[:4] + "****" + token[len(token)-4:]
}

type modelConfigRequest struct {
	ModelType int8   `json:"model_type"`
	Model     string `json:"model"`
	BaseURL   string `json:"base_url"`
	Token     string `json:"token"`
}

// Save upserts the model config for the current user.
func (h *ModelConfigHandler) Save(c *gin.Context) {
	ctx := c.Request.Context()
	var req modelConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}
	if req.Model == "" || req.Token == "" {
		response.BadRequest(c, "model and token are required")
		return
	}
	if req.ModelType != models.ModelTypeChat && req.ModelType != models.ModelTypeEmbedding {
		response.BadRequest(c, "model_type must be 1 (chat) or 2 (embedding)")
		return
	}

	userID := c.GetInt64(middleware.CtxUserID)
	cfg, err := h.store.Save(ctx, userID, req.ModelType, req.Model, req.BaseURL, req.Token)
	if err != nil {
		logger.L().Error("save model config failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, err.Error())
		return
	}
	response.OK(c, cfg)
}
