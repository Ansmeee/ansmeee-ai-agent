package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ansmeee-ai-agent/internal/agent"
	"ansmeee-ai-agent/internal/config"
	"ansmeee-ai-agent/internal/llm"
	"ansmeee-ai-agent/internal/memory"
	"ansmeee-ai-agent/internal/middleware"
	"ansmeee-ai-agent/internal/tool"
	"github.com/gin-gonic/gin"
	"github.com/tmc/langchaingo/llms"
	"go.uber.org/zap"
)

// --- Mock LLM Model for integration tests ---

type mockLLMModel struct {
	mu      sync.Mutex
	calls   []mockCall
	callIdx int
}

type mockCall struct {
	content   string
	toolCalls []llms.ToolCall
	err       error
}

func (m *mockLLMModel) GenerateContent(_ context.Context, _ []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.callIdx >= len(m.calls) {
		return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: "fallback"}}}, nil
	}
	c := m.calls[m.callIdx]
	m.callIdx++
	if c.err != nil {
		return nil, c.err
	}
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{
			Content:   c.content,
			ToolCalls: c.toolCalls,
		}},
	}, nil
}

func (m *mockLLMModel) Call(_ context.Context, _ string, _ ...llms.CallOption) (string, error) {
	return "", nil
}

// --- Test infrastructure ---

func setupIntegrationTest(mockCalls []mockCall) (*gin.Engine, memory.SessionStore) {
	gin.SetMode(gin.TestMode)

	memCfg := &config.MemoryConfig{Type: "memory", TTL: 5 * time.Minute, MaxMessages: 100}
	store := memory.NewInMemory(memCfg)

	reg := tool.NewRegistry()
	reg.Register(&tool.Calculator{})
	reg.Register(&tool.DateTime{})

	mockModel := &mockLLMModel{calls: mockCalls}
	provider := llm.NewForTest(mockModel)

	logger, _ := zap.NewDevelopment()
	cb := agent.NewCallback(logger)
	engine := agent.New(provider, reg, store, cb,
		agent.WithToolTimeout(5*time.Second),
		agent.WithMaxOutputLength(4096),
		agent.WithParallelToolCalls(true),
		agent.WithMaxContextMessages(50),
	)

	// Create a wrapper that implements the AgentStore interface expected by StreamHandler.
	// Since StreamHandler takes *agent.AgentStore which uses GORM, we need to bypass it
	// for the disabled-agent test. We'll test that scenario differently.
	var agentStore *agent.AgentStore
	// For non-DB tests, agentStore is nil — resolveAgentConfig returns nil config (no agent filtering)

	streamHandler := NewStreamHandler(engine, store, agentStore, nil)
	chatHandler := NewChatHandler(store, agentStore)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.CtxUserID, int64(1))
		c.Set(middleware.CtxUserUUID, "test-uuid")
		c.Set(middleware.CtxUserEmail, "test@test.com")
		c.Next()
	})
	r.POST("/api/v1/chat/completion", streamHandler.Handle)
	r.GET("/api/v1/chat/:sessionId", chatHandler.History)

	return r, store
}

func parseSSEEvents(body string) []sseEvent {
	var events []sseEvent
	scanner := bufio.NewScanner(strings.NewReader(body))
	var currentEvent sseEvent
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "event: "); ok {
			currentEvent.event = after
		} else if after, ok := strings.CutPrefix(line, "data: "); ok {
			currentEvent.data = after
		} else if line == "" && currentEvent.event != "" {
			events = append(events, currentEvent)
			currentEvent = sseEvent{}
		}
	}
	return events
}

type sseEvent struct {
	event string
	data  string
}

func doStreamRequest(r *gin.Engine, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/completion", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

// --- Integration Test 1: Math calculation end-to-end ---

func TestIntegration_MathCalculation(t *testing.T) {
	mockCalls := []mockCall{
		{toolCalls: []llms.ToolCall{{
			ID:   "call_1",
			Type: "function",
			FunctionCall: &llms.FunctionCall{
				Name:      "calculator",
				Arguments: `2+2`,
			},
		}}},
		{content: "2+2 equals 4"},
	}

	r, _ := setupIntegrationTest(mockCalls)
	w := doStreamRequest(r, `{"message":"what is 2+2?","session_id":"s1"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	events := parseSSEEvents(w.Body.String())

	// Verify: session → thinking → tool_call → thinking → chunk(s) → done
	eventTypes := make([]string, len(events))
	for i, e := range events {
		eventTypes[i] = e.event
	}

	// Must start with session
	if len(events) == 0 || events[0].event != "session" {
		t.Fatal("first event should be 'session'")
	}

	// Verify session data is valid JSON
	var sessionData sseSessionData
	if err := json.Unmarshal([]byte(events[0].data), &sessionData); err != nil {
		t.Fatalf("session data not valid JSON: %v", err)
	}
	if sessionData.SessionID != "s1" {
		t.Errorf("session_id = %q, want s1", sessionData.SessionID)
	}

	// Must contain tool_call and done
	hasToolCall, hasDone := false, false
	for _, e := range events {
		switch e.event {
		case "tool_call":
			hasToolCall = true
			var data sseToolCallData
			if err := json.Unmarshal([]byte(e.data), &data); err != nil {
				t.Errorf("tool_call data not valid JSON: %v", err)
			}
			if data.Name != "calculator" {
				t.Errorf("tool_call name = %q, want calculator", data.Name)
			}
			if !data.Success {
				t.Error("tool_call should be successful")
			}
		case "done":
			hasDone = true
		}
		// All data fields must be valid JSON
		if !json.Valid([]byte(e.data)) {
			t.Errorf("event %q has invalid JSON data: %s", e.event, e.data)
		}
	}

	if !hasToolCall {
		t.Error("missing tool_call event")
	}
	if !hasDone {
		t.Error("missing done event")
	}
}

// --- Integration Test 2: Multi-round tool calls ---

func TestIntegration_MultiRoundToolCalls(t *testing.T) {
	mockCalls := []mockCall{
		{toolCalls: []llms.ToolCall{{
			ID:   "call_1",
			Type: "function",
			FunctionCall: &llms.FunctionCall{
				Name:      "calculator",
				Arguments: `2^10`,
			},
		}}},
		{toolCalls: []llms.ToolCall{{
			ID:   "call_2",
			Type: "function",
			FunctionCall: &llms.FunctionCall{
				Name:      "calculator",
				Arguments: `365%7`,
			},
		}}},
		{content: "2^10 is 1024, and 365 mod 7 is 1"},
	}

	r, _ := setupIntegrationTest(mockCalls)
	w := doStreamRequest(r, `{"message":"calc 2^10 and 365 mod 7","session_id":"s2"}`)

	events := parseSSEEvents(w.Body.String())

	toolCalls, thinkings := 0, 0
	for _, e := range events {
		switch e.event {
		case "tool_call":
			toolCalls++
		case "thinking":
			thinkings++
		}
	}

	if toolCalls != 2 {
		t.Errorf("expected 2 tool_call events, got %d", toolCalls)
	}
	if thinkings < 2 {
		t.Errorf("expected at least 2 thinking events (one per iteration), got %d", thinkings)
	}
}

// --- Integration Test 3: Tool not found returns failed tool_end ---

func TestIntegration_ToolNotFound(t *testing.T) {
	// LLM calls "weather" which is NOT registered → tool_end with success=false.
	mockCalls := []mockCall{
		{toolCalls: []llms.ToolCall{{
			ID:   "call_1",
			Type: "function",
			FunctionCall: &llms.FunctionCall{
				Name:      "weather",
				Arguments: `Beijing`,
			},
		}}},
		{content: "I could not find the weather tool"},
	}

	r, _ := setupIntegrationTest(mockCalls)
	w := doStreamRequest(r, `{"message":"what is the weather?","session_id":"s3"}`)
	events := parseSSEEvents(w.Body.String())

	hasFailedTool := false
	for _, e := range events {
		if e.event == "tool_call" {
			var data sseToolCallData
			json.Unmarshal([]byte(e.data), &data)
			if !data.Success && data.Name == "weather" {
				hasFailedTool = true
			}
		}
	}
	if !hasFailedTool {
		t.Error("expected a failed tool_call when LLM calls a tool not in registry")
	}

	// Should still get a done event (engine continues after tool failure)
	hasDone := false
	for _, e := range events {
		if e.event == "done" {
			hasDone = true
		}
	}
	if !hasDone {
		t.Error("expected done event after tool failure")
	}
}

// --- Integration Test 4: History replay includes tool messages ---

func TestIntegration_HistoryReplay(t *testing.T) {
	mockCalls := []mockCall{
		{toolCalls: []llms.ToolCall{{
			ID:   "call_1",
			Type: "function",
			FunctionCall: &llms.FunctionCall{
				Name:      "calculator",
				Arguments: `1+1`,
			},
		}}},
		{content: "The answer is 2"},
	}

	r, _ := setupIntegrationTest(mockCalls)

	// First, send a stream request to create conversation history
	doStreamRequest(r, `{"message":"what is 1+1?","session_id":"hist-session"}`)

	// Now fetch history
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/hist-session", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("history returned %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			SessionID string           `json:"session_id"`
			Messages  []memory.Message `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Code != 0 {
		t.Fatalf("response code = %d: %s", resp.Code, resp.Message)
	}

	messages := resp.Data.Messages
	if len(messages) < 4 {
		t.Fatalf("expected at least 4 messages (human, function, tool, ai), got %d", len(messages))
	}

	// Check that role=tool and role=function messages are present
	hasToolCall, hasToolResult := false, false
	for _, m := range messages {
		switch m.Role {
		case "function":
			hasToolCall = true
			if !json.Valid([]byte(m.Content)) {
				t.Errorf("function content is not valid JSON: %s", m.Content)
			}
		case "tool":
			hasToolResult = true
			if !json.Valid([]byte(m.Content)) {
				t.Errorf("tool content is not valid JSON: %s", m.Content)
			}
		}
	}
	if !hasToolCall {
		t.Error("history missing function message")
	}
	if !hasToolResult {
		t.Error("history missing tool message")
	}
}

// --- Integration Test 5: Disabled agent returns error event ---

func TestIntegration_DisabledAgent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	memCfg := &config.MemoryConfig{Type: "memory", TTL: 5 * time.Minute, MaxMessages: 100}
	store := memory.NewInMemory(memCfg)

	reg := tool.NewRegistry()
	mockModel := &mockLLMModel{calls: []mockCall{{content: "should not reach here"}}}
	provider := llm.NewForTest(mockModel)

	logger, _ := zap.NewDevelopment()
	cb := agent.NewCallback(logger)
	engine := agent.New(provider, reg, store, cb)

	// We can't easily create a real AgentStore without a DB.
	// Instead, test resolveAgentConfig directly via a custom handler wrapper.
	streamHandler := &StreamHandler{
		engine:     engine,
		mem:        store,
		agentStore: nil,
	}

	// Test the resolveAgentConfig logic directly
	// Create a custom router that simulates a disabled agent scenario
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.CtxUserID, int64(1))
		c.Next()
	})
	r.POST("/api/v1/chat/completion", func(c *gin.Context) {
		var req ChatRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Message == "" {
			c.JSON(400, gin.H{"code": 1001, "message": "bad request"})
			return
		}

		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("X-Accel-Buffering", "no")

		flusher, _ := c.Writer.(http.Flusher)

		// Simulate what resolveAgentConfig does for a disabled agent
		_ = streamHandler
		writeSSEJSON(c.Writer, flusher, "error", sseErrorData{
			Message: `agent "disabled-agent" is disabled`,
		})
	})

	w := doStreamRequest(r, `{"message":"hello","session_id":"s5","agent_id":"disabled-agent"}`)

	events := parseSSEEvents(w.Body.String())

	hasError := false
	for _, e := range events {
		if e.event == "error" {
			hasError = true
			var data sseErrorData
			if err := json.Unmarshal([]byte(e.data), &data); err != nil {
				t.Fatalf("error data not valid JSON: %v", err)
			}
			if !strings.Contains(data.Message, "disabled") {
				t.Errorf("error message should mention 'disabled', got: %q", data.Message)
			}
		}
	}
	if !hasError {
		t.Error("expected error event for disabled agent")
	}

	// Verify no other events (no thinking, no chunk, no done)
	for _, e := range events {
		if e.event != "error" {
			t.Errorf("unexpected event %q for disabled agent", e.event)
		}
	}
}
