package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"ansmeee-ai-agent/internal/agent"
	"ansmeee-ai-agent/internal/llm"
	"ansmeee-ai-agent/internal/memory"
	"ansmeee-ai-agent/internal/middleware"
	"ansmeee-ai-agent/internal/models"
	"ansmeee-ai-agent/internal/tracing"
	"ansmeee-ai-agent/pkg/logger"
	"ansmeee-ai-agent/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// SSE data types for type-safe JSON serialization.
type sseChunkData struct {
	Content string `json:"content"`
}
type sseThinkingData struct {
	Iteration int `json:"iteration"`
}
type sseToolCallData struct {
	ToolCallID string          `json:"tool_call_id"`
	Name       string          `json:"name"`
	Arguments  json.RawMessage `json:"arguments"`
	Result     json.RawMessage `json:"result"`
	Success    bool            `json:"success"`
}
type sseSessionData struct {
	SessionID string `json:"session_id"`
}
type sseErrorData struct {
	Message string `json:"message"`
}

// StreamHandler handles SSE streaming chat requests.
type StreamHandler struct {
	engine           *agent.Engine
	mem              memory.SessionStore
	agentStore       *agent.AgentStore
	modelConfigStore *llm.ModelConfigStore
}

// NewStreamHandler creates a new stream handler.
func NewStreamHandler(engine *agent.Engine, mem memory.SessionStore, agentStore *agent.AgentStore, modelConfigStore *llm.ModelConfigStore) *StreamHandler {
	return &StreamHandler{engine: engine, mem: mem, agentStore: agentStore, modelConfigStore: modelConfigStore}
}

func (h *StreamHandler) resolveAgentConfig(ctx context.Context, agentID string, userID int64) (*agent.AgentConfig, error) {
	if agentID == "" || h.agentStore == nil {
		return nil, nil
	}
	a, err := h.agentStore.Get(ctx, agentID, userID)
	if err != nil {
		if errors.Is(err, agent.ErrAgentNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("resolve agent: %w", err)
	}
	if a.Status == models.AgentStatusDisabled {
		return nil, fmt.Errorf("agent %q is disabled", agentID)
	}
	cfg := &agent.AgentConfig{
		AgentID:       a.UUID,
		Prompt:        a.Prompt,
		Tools:         []string(a.Tools),
		Model:         a.Model,
		Temperature:   a.Temperature,
		MaxTokens:     a.MaxTokens,
		TopP:          a.TopP,
		MaxIterations: int(a.MaxIterations),
	}
	return cfg, nil
}

// Handle processes a streaming chat request via SSE.
func (h *StreamHandler) Handle(c *gin.Context) {
	ctx := c.Request.Context()
	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "message is required")
		return
	}
	if req.Message == "" {
		response.BadRequest(c, "message is required")
		return
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	// Set SSE headers.
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		response.InternalError(c, "streaming not supported")
		return
	}

	// Record agent for this session.
	userID := c.GetInt64(middleware.CtxUserID)
	if req.AgentID != "" {
		if err := h.mem.SetAgent(ctx, sessionID, req.AgentID, userID); err != nil {
			logger.L().Warn("set agent for session failed", tracing.ErrFields(ctx, err)...)
		}
	}

	// Resolve agent config (includes status check).
	agentCfg, err := h.resolveAgentConfig(ctx, req.AgentID, userID)
	if err != nil {
		logger.L().Error("resolve agent config failed", tracing.ErrFields(ctx, err)...)
		writeSSEJSON(c.Writer, flusher, "error", sseErrorData{Message: err.Error()})
		return
	}

	// Send session_id as first event.
	writeSSEJSON(c.Writer, flusher, "session", sseSessionData{SessionID: sessionID})

	// Resolve user model config.
	var modelCfg *models.ModelConfig
	if h.modelConfigStore != nil {
		var err error
		modelCfg, err = h.modelConfigStore.GetByUserAndType(ctx, userID, models.ModelTypeChat)
		if err != nil {
			logger.L().Warn("get model config failed", tracing.ErrFields(ctx, err)...)
			// Non-fatal: continue with default config.
		}
	}

	// Process stream.
	ch := h.engine.ProcessStream(ctx, sessionID, req.Message, agentCfg, modelCfg, userID)

	for evt := range ch {
		switch evt.Type {
		case "chunk":
			writeSSEJSON(c.Writer, flusher, "chunk", sseChunkData{Content: evt.Content})
		case "thinking":
			writeSSEJSON(c.Writer, flusher, "thinking", sseThinkingData{Iteration: evt.Iteration})
		case "tool_call":
			writeSSEJSON(c.Writer, flusher, "tool_call", sseToolCallData{
				ToolCallID: evt.ToolCallID,
				Name:       evt.ToolName,
				Arguments:  ensureJSON(evt.Arguments),
				Result:     ensureJSON(evt.Result),
				Success:    evt.Success,
			})
		case "done":
			writeSSEJSON(c.Writer, flusher, "done", sseSessionData{SessionID: evt.Content})
			return
		case "error":
			writeSSEJSON(c.Writer, flusher, "error", sseErrorData{Message: evt.Error.Error()})
			return
		}
	}
}

// writeSSEJSON serializes data as JSON and writes it as an SSE event.
func writeSSEJSON(w http.ResponseWriter, flusher http.Flusher, event string, data any) {
	b, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	flusher.Flush()
}

// ensureJSON ensures the string is valid JSON; if not, wraps it as a JSON string.
func ensureJSON(s string) json.RawMessage {
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	b, _ := json.Marshal(s)
	return json.RawMessage(b)
}
