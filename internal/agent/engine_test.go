package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"ansmeee-ai-agent/internal/config"
	"ansmeee-ai-agent/internal/llm"
	"ansmeee-ai-agent/internal/memory"
	"ansmeee-ai-agent/internal/models"
	"ansmeee-ai-agent/internal/tool"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/tools"
	"go.uber.org/zap"
)

// --- Mock Tool ---

type mockTool struct {
	name        string
	output      string
	err         error
	delay       time.Duration
	shouldPanic bool
	callCount   int
}

func (t *mockTool) Name() string        { return t.name }
func (t *mockTool) Description() string { return "mock tool: " + t.name }
func (t *mockTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *mockTool) Call(ctx context.Context, input string) (string, error) {
	t.callCount++
	if t.shouldPanic {
		panic("mock panic")
	}
	if t.delay > 0 {
		select {
		case <-time.After(t.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return t.output, t.err
}

// --- Mock LLM call sequence ---

type mockLLMCall struct {
	content   string
	toolCalls []llms.ToolCall
	err       error
}

// --- Helper to build a tool.Registry with mock tools ---

func newTestToolRegistry(mockTools ...*mockTool) *tool.Registry {
	reg := tool.NewRegistry()
	for _, mt := range mockTools {
		reg.Register(mt)
	}
	return reg
}

func makeTc(id, name, args string) llms.ToolCall {
	return llms.ToolCall{
		ID:   id,
		Type: "function",
		FunctionCall: &llms.FunctionCall{
			Name:      name,
			Arguments: args,
		},
	}
}

// --- Direct method tests (no LLM needed) ---

func TestResolveTools_FilterByNames(t *testing.T) {
	calc := &mockTool{name: "calculator", output: "4"}
	dt := &mockTool{name: "datetime", output: "now"}
	reg := newTestToolRegistry(calc, dt)
	e := &Engine{registry: reg}

	// nil agentCfg → nil tools
	result := e.resolveTools(nil)
	if result != nil {
		t.Error("expected nil tools for nil config")
	}

	// empty tools list → nil
	result = e.resolveTools(&AgentConfig{Tools: []string{}})
	if result != nil {
		t.Error("expected nil tools for empty list")
	}

	// specific names
	result = e.resolveTools(&AgentConfig{Tools: []string{"calculator"}})
	if len(result) != 1 || result[0].Name() != "calculator" {
		t.Errorf("expected [calculator], got %v", result)
	}

	// nonexistent tool → filtered out
	result = e.resolveTools(&AgentConfig{Tools: []string{"nonexistent"}})
	if len(result) != 0 {
		t.Errorf("expected empty for nonexistent, got %d", len(result))
	}
}

func TestResolveLLMProvider_Priority(t *testing.T) {
	cfg := &config.LLMConfig{
		Model:   "default-model",
		BaseURL: "http://default",
		APIKey:  "default-key",
	}
	defaultProvider, _ := llm.New(cfg)
	e := &Engine{llm: defaultProvider}

	// No overrides → default
	p := e.resolveLLMProvider(nil, nil)
	if p != defaultProvider {
		t.Error("expected default provider when no overrides")
	}

	// User config with empty model → default
	p = e.resolveLLMProvider(nil, &models.ModelConfig{Model: ""})
	if p != defaultProvider {
		t.Error("expected default provider when user model is empty")
	}
}

func TestBuildMessages_WithToolHistory(t *testing.T) {
	e := &Engine{maxContextMessages: 50}

	history := []memory.Message{
		{Role: models.RoleHuman, Content: "what is 2+2?"},
		{Role: models.RoleFunction, Content: `{"tool_calls":[{"id":"c1","name":"calculator","arguments":"2+2"}]}`},
		{Role: models.RoleTool, Content: `{"tool_call_id":"c1","name":"calculator","result":"4"}`},
		{Role: models.RoleAI, Content: "The answer is 4."},
	}

	msgs := e.buildMessages("system prompt", history)

	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(msgs))
	}
	if msgs[0].Role != llm.RoleSystem {
		t.Error("first message should be system")
	}
	if msgs[1].Role != llm.RoleUser {
		t.Error("second message should be user")
	}
	if msgs[2].Role != llm.RoleAssistant {
		t.Error("third message should be assistant (tool_call)")
	}
	if len(msgs[2].ToolCalls) != 1 {
		t.Error("assistant message should have 1 tool call")
	}
	if msgs[3].Role != llm.RoleTool {
		t.Error("fourth message should be tool")
	}
	if msgs[3].ToolCallID != "c1" {
		t.Errorf("tool message ToolCallID = %q, want c1", msgs[3].ToolCallID)
	}
	if msgs[4].Role != llm.RoleAssistant {
		t.Error("fifth message should be assistant")
	}
}

func TestBuildMessages_EmptyHistory(t *testing.T) {
	e := &Engine{maxContextMessages: 50}
	msgs := e.buildMessages("prompt", nil)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message (system), got %d", len(msgs))
	}
	if msgs[0].Content != "prompt" {
		t.Errorf("system prompt = %q", msgs[0].Content)
	}
}

func TestBuildMessages_InvalidJSON(t *testing.T) {
	e := &Engine{maxContextMessages: 50}
	history := []memory.Message{
		{Role: models.RoleFunction, Content: "not json"},
		{Role: models.RoleTool, Content: "also not json"},
		{Role: models.RoleAI, Content: "final"},
	}
	msgs := e.buildMessages("sys", history)
	// Invalid JSON messages are silently skipped; only system + assistant remain
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
}

func TestTrimHistory_KeepPairs(t *testing.T) {
	history := []memory.Message{
		{Role: models.RoleHuman, Content: "msg1"},
		{Role: models.RoleFunction, Content: "tc1"},
		{Role: models.RoleTool, Content: "tr1"},
		{Role: models.RoleAI, Content: "reply1"},
		{Role: models.RoleHuman, Content: "msg2"},
		{Role: models.RoleAI, Content: "reply2"},
	}

	// Trim to 3 — start=3 → history[3] is "assistant" (reply1), no back-up needed
	trimmed := trimHistory(history, 3)
	if len(trimmed) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(trimmed))
	}
	if trimmed[0].Role != models.RoleAI || trimmed[0].Content != "reply1" {
		t.Errorf("first trimmed = %v, expected reply1", trimmed[0])
	}

	// Trim to 2 — start=4 → history[4] is "user", no pair issue
	trimmed = trimHistory(history, 2)
	if len(trimmed) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(trimmed))
	}

	// No trim needed
	trimmed = trimHistory(history, 100)
	if len(trimmed) != 6 {
		t.Fatalf("expected 6 messages, got %d", len(trimmed))
	}

	// Trim to 4 — start=2 → history[2] is "tool", back up to include tool_call at index 1
	trimmed = trimHistory(history, 4)
	if len(trimmed) != 5 {
		t.Fatalf("expected 5 messages (backed up to include tool_call), got %d", len(trimmed))
	}
	if trimmed[0].Role != models.RoleFunction {
		t.Errorf("first trimmed should be function (tool_call carrier), got %s", trimmed[0].Role)
	}
}

func TestBuildChatOptions(t *testing.T) {
	e := &Engine{}

	// nil config → nil options
	opts := e.buildChatOptions(nil)
	if opts != nil {
		t.Error("expected nil for nil config")
	}

	// nil ModelConfig → nil options
	opts = e.buildChatOptions(&AgentConfig{})
	if opts != nil {
		t.Error("expected nil for empty AgentConfig")
	}

	// Full config
	temp := 0.5
	topP := 0.9
	opts = e.buildChatOptions(&AgentConfig{
		Temperature: &temp,
		MaxTokens:   1000,
		TopP:        &topP,
	})
	if len(opts) != 3 {
		t.Errorf("expected 3 options, got %d", len(opts))
	}

	// Temperature=0 should still produce an option (pointer is non-nil)
	zero := 0.0
	opts = e.buildChatOptions(&AgentConfig{
		Temperature: &zero,
	})
	if len(opts) != 1 {
		t.Errorf("expected 1 option for temp=0, got %d", len(opts))
	}
}

func TestToolCallToLLMMessage(t *testing.T) {
	tc := llms.ToolCall{
		ID:   "call_1",
		Type: "function",
		FunctionCall: &llms.FunctionCall{
			Name:      "calc",
			Arguments: `{"x":1}`,
		},
	}
	result := &llm.ChatResult{
		Content:   "Let me calculate",
		ToolCalls: []llms.ToolCall{tc},
	}

	msg := toolCallToLLMMessage(result)
	if msg.Role != llm.RoleAssistant {
		t.Errorf("role = %s, want assistant", msg.Role)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].ID != "call_1" {
		t.Error("tool call ID mismatch")
	}
}

// --- testableEngine: duplicates ProcessStream logic with a mock chat function ---

type testableEngine struct {
	*Engine
	chatFunc func(ctx context.Context, messages []llm.MessageContent, toolList []tools.Tool, opts ...llm.ChatOption) (*llm.ChatResult, error)
}

func newTestableEngine(chatCalls []mockLLMCall, mockTools ...*mockTool) *testableEngine {
	memCfg := &config.MemoryConfig{Type: "memory", TTL: 5 * time.Minute, MaxMessages: 100}
	store := memory.NewInMemory(memCfg)

	reg := newTestToolRegistry(mockTools...)
	logger, _ := zap.NewDevelopment()
	cb := NewCallback(logger)

	callIdx := 0
	te := &testableEngine{}
	te.Engine = &Engine{
		registry:           reg,
		mem:                store,
		callback:           cb,
		systemPrompt:       "test",
		maxIter:            5,
		toolTimeout:        2 * time.Second,
		maxOutputLength:    4096,
		parallelToolCalls:  true,
		maxContextMessages: 50,
	}
	te.chatFunc = func(_ context.Context, _ []llm.MessageContent, _ []tools.Tool, _ ...llm.ChatOption) (*llm.ChatResult, error) {
		if callIdx >= len(chatCalls) {
			return &llm.ChatResult{Content: "fallback"}, nil
		}
		c := chatCalls[callIdx]
		callIdx++
		if c.err != nil {
			return nil, c.err
		}
		return &llm.ChatResult{Content: c.content, ToolCalls: c.toolCalls}, nil
	}
	return te
}

func (te *testableEngine) processStream(ctx context.Context, sessionID, msg string, agentCfg *AgentConfig, userID int64) <-chan StreamEvent {
	ch := make(chan StreamEvent, 20)

	go func() {
		defer close(ch)
		defer func() {
			if r := recover(); r != nil {
				ch <- StreamEvent{Type: "error", Error: fmt.Errorf("panic: %v", r)}
			}
		}()

		prompt := te.systemPrompt
		if agentCfg != nil && agentCfg.Prompt != "" {
			prompt = agentCfg.Prompt
		}
		maxIter := te.maxIter
		if agentCfg != nil && agentCfg.MaxIterations > 0 {
			maxIter = agentCfg.MaxIterations
		}

		var task *memory.TaskState
		var turnDelta []memory.Message
		if te.memMgr != nil {
			task = te.memMgr.Load(ctx, sessionID)
		}
		turnDelta = append(turnDelta, memory.Message{Role: models.RoleHuman, Content: msg})

		if err := te.mem.AddMessage(ctx, sessionID, memory.Message{Role: models.RoleHuman, Content: msg}, userID); err != nil {
			ch <- StreamEvent{Type: "error", Error: err}
			return
		}

		history, _ := te.mem.History(ctx, sessionID)
		const memAgentID = ""
		if te.memMgr != nil {
			enrich := te.memMgr.Retrieve(ctx, userID, memAgentID, sessionID, msg, task)
			prompt = appendEnrichment(prompt, enrich)
			if task != nil {
				memory.UpdateTaskState(task, memory.UserMessageEvent(msg))
			}
		}
		messages := te.buildMessages(prompt, history)
		filteredTools := te.resolveTools(agentCfg)

		parallelToolCalls := te.parallelToolCalls
		if agentCfg != nil && agentCfg.ParallelToolCalls != nil {
			parallelToolCalls = *agentCfg.ParallelToolCalls
		}

		for iter := 0; iter < maxIter; iter++ {
			ch <- StreamEvent{Type: "thinking", Iteration: iter + 1}
			te.callback.OnLLMStart(ctx, sessionID)

			result, err := te.chatFunc(ctx, messages, filteredTools)
			if err != nil {
				ch <- StreamEvent{Type: "error", Error: err}
				return
			}

			if len(result.ToolCalls) == 0 {
				if result.Content != "" {
					ch <- StreamEvent{Type: "chunk", Content: result.Content}
				}
				te.mem.AddMessage(ctx, sessionID, memory.Message{
					Role: models.RoleAI, Content: result.Content,
				}, userID)
				te.callback.OnLLMEnd(ctx, sessionID, 0, 0)
				te.finalizeTask(ctx, task, turnDelta, sessionID, userID, result.Content)
				ch <- StreamEvent{Type: "done", Content: sessionID}
				return
			}

			toolCallMsg := memory.Message{
				Role:    models.RoleFunction,
				Content: buildToolCallJSON(result.ToolCalls),
			}
			te.mem.AddMessage(ctx, sessionID, toolCallMsg, userID)
			messages = append(messages, toolCallToLLMMessage(result))

			if te.memMgr != nil && task != nil {
				for _, tc := range result.ToolCalls {
					name := ""
					if tc.FunctionCall != nil {
						name = tc.FunctionCall.Name
					}
					memory.UpdateTaskState(task, memory.ToolCallEvent(name))
				}
				turnDelta = append(turnDelta, toolCallMsg)
			}

			var toolMsgs []llm.MessageContent
			if parallelToolCalls && len(result.ToolCalls) > 1 {
				toolMsgs = te.executeToolsConcurrently(ctx, ch, result.ToolCalls, sessionID, userID)
			} else {
				for _, tc := range result.ToolCalls {
					toolMsgs = append(toolMsgs, te.executeAndEmitTool(ctx, ch, tc, sessionID, userID))
				}
			}
			messages = append(messages, toolMsgs...)

			if te.memMgr != nil && task != nil {
				for _, tm := range toolMsgs {
					success := !strings.HasPrefix(tm.Content, "Error:")
					memory.UpdateTaskState(task, memory.ToolResultEvent(tm.ToolCallName, truncateSummary(tm.Content), success))
				}
			}
			te.callback.OnLLMEnd(ctx, sessionID, 0, 0)
		}

		ch <- StreamEvent{Type: "thinking", Content: "max iterations reached"}
		result, err := te.chatFunc(ctx, messages, nil)
		if err != nil {
			ch <- StreamEvent{Type: "error", Error: err}
			return
		}
		if result.Content != "" {
			ch <- StreamEvent{Type: "chunk", Content: result.Content}
		}
		te.mem.AddMessage(ctx, sessionID, memory.Message{
			Role: "assistant", Content: result.Content,
		}, userID)
		te.callback.OnLLMEnd(ctx, sessionID, 0, 0)
		te.finalizeTask(ctx, task, turnDelta, sessionID, userID, result.Content)
		ch <- StreamEvent{Type: "done", Content: sessionID}
	}()

	return ch
}

// --- Helpers ---

func collectEvents(ch <-chan StreamEvent) []StreamEvent {
	var events []StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}
	return events
}

func findEventTypes(events []StreamEvent) []string {
	var types []string
	for _, e := range events {
		types = append(types, e.Type)
	}
	return types
}

func containsStr(strs []string, target string) bool {
	for _, s := range strs {
		if s == target {
			return true
		}
	}
	return false
}

// --- ProcessStream integration tests ---

func TestProcessStream_NoTools(t *testing.T) {
	te := newTestableEngine(
		[]mockLLMCall{{content: "Hello!"}},
	)

	ch := te.processStream(context.Background(), "s1", "hi", nil, 1)
	events := collectEvents(ch)
	types := findEventTypes(events)

	if !containsStr(types, "thinking") {
		t.Error("missing thinking event")
	}
	if !containsStr(types, "chunk") {
		t.Error("missing chunk event")
	}
	if !containsStr(types, "done") {
		t.Error("missing done event")
	}
	if containsStr(types, "tool_call") {
		t.Error("unexpected tool_call in no-tools mode")
	}
}

func TestProcessStream_SingleToolCall(t *testing.T) {
	calc := &mockTool{name: "calculator", output: "4"}
	te := newTestableEngine(
		[]mockLLMCall{
			{toolCalls: []llms.ToolCall{makeTc("c1", "calculator", "2+2")}},
			{content: "The answer is 4"},
		},
		calc,
	)

	cfg := &AgentConfig{Tools: []string{"calculator"}}
	ch := te.processStream(context.Background(), "s1", "what is 2+2?", cfg, 1)
	events := collectEvents(ch)
	types := findEventTypes(events)

	if !containsStr(types, "tool_call") {
		t.Error("missing tool_call")
	}
	if calc.callCount != 1 {
		t.Errorf("calculator called %d times, want 1", calc.callCount)
	}
}

func TestProcessStream_MultiRound(t *testing.T) {
	calc := &mockTool{name: "calculator", output: "4"}
	te := newTestableEngine(
		[]mockLLMCall{
			{toolCalls: []llms.ToolCall{makeTc("c1", "calculator", "2+2")}},
			{toolCalls: []llms.ToolCall{makeTc("c2", "calculator", "4*3")}},
			{content: "Final answer: 12"},
		},
		calc,
	)

	cfg := &AgentConfig{Tools: []string{"calculator"}}
	ch := te.processStream(context.Background(), "s1", "complex calc", cfg, 1)
	events := collectEvents(ch)

	toolCalls := 0
	for _, e := range events {
		if e.Type == "tool_call" {
			toolCalls++
		}
	}
	if toolCalls != 2 {
		t.Errorf("expected 2 tool_call events, got %d", toolCalls)
	}
}

func TestProcessStream_ParallelTools(t *testing.T) {
	calc := &mockTool{name: "calculator", output: "4"}
	dt := &mockTool{name: "datetime", output: "2024-01-01"}
	te := newTestableEngine(
		[]mockLLMCall{
			{toolCalls: []llms.ToolCall{makeTc("c1", "calculator", "2+2"), makeTc("c2", "datetime", "now")}},
			{content: "Done"},
		},
		calc, dt,
	)
	te.parallelToolCalls = true

	cfg := &AgentConfig{Tools: []string{"calculator", "datetime"}}
	ch := te.processStream(context.Background(), "s1", "calc and date", cfg, 1)
	events := collectEvents(ch)

	toolCalls := 0
	for _, e := range events {
		if e.Type == "tool_call" {
			toolCalls++
		}
	}
	if toolCalls != 2 {
		t.Errorf("expected 2 tool_call events, got %d", toolCalls)
	}
	if calc.callCount != 1 || dt.callCount != 1 {
		t.Errorf("calc=%d dt=%d, both should be 1", calc.callCount, dt.callCount)
	}
}

func TestProcessStream_SerialTools(t *testing.T) {
	calc := &mockTool{name: "calculator", output: "4"}
	dt := &mockTool{name: "datetime", output: "2024-01-01"}
	te := newTestableEngine(
		[]mockLLMCall{
			{toolCalls: []llms.ToolCall{makeTc("c1", "calculator", "2+2"), makeTc("c2", "datetime", "now")}},
			{content: "Done"},
		},
		calc, dt,
	)

	pfalse := false
	cfg := &AgentConfig{Tools: []string{"calculator", "datetime"}, ParallelToolCalls: &pfalse}
	ch := te.processStream(context.Background(), "s1", "calc and date", cfg, 1)
	events := collectEvents(ch)

	if calc.callCount != 1 || dt.callCount != 1 {
		t.Errorf("calc=%d dt=%d, both should be 1", calc.callCount, dt.callCount)
	}
	if !containsStr(findEventTypes(events), "done") {
		t.Error("missing done event")
	}
}

func TestProcessStream_ToolTimeout(t *testing.T) {
	slow := &mockTool{name: "slow", output: "late", delay: 5 * time.Second}
	te := newTestableEngine(
		[]mockLLMCall{
			{toolCalls: []llms.ToolCall{makeTc("c1", "slow", "go")}},
			{content: "Tool timed out"},
		},
		slow,
	)
	te.toolTimeout = 100 * time.Millisecond

	cfg := &AgentConfig{Tools: []string{"slow"}}
	ch := te.processStream(context.Background(), "s1", "run slow", cfg, 1)
	events := collectEvents(ch)

	found := false
	for _, e := range events {
		if e.Type == "tool_call" && !e.Success {
			found = true
		}
	}
	if !found {
		t.Error("expected failed tool_call event for timeout")
	}
}

func TestProcessStream_ToolPanic(t *testing.T) {
	panicker := &mockTool{name: "panicker", shouldPanic: true}
	te := newTestableEngine(
		[]mockLLMCall{
			{toolCalls: []llms.ToolCall{makeTc("c1", "panicker", "go")}},
			{content: "Recovered"},
		},
		panicker,
	)

	cfg := &AgentConfig{Tools: []string{"panicker"}}
	ch := te.processStream(context.Background(), "s1", "panic", cfg, 1)
	events := collectEvents(ch)

	found := false
	for _, e := range events {
		if e.Type == "tool_call" && !e.Success && strings.Contains(e.Result, "panic") {
			found = true
		}
	}
	if !found {
		t.Error("expected tool_call with panic error")
	}
}

func TestProcessStream_MaxIter(t *testing.T) {
	calc := &mockTool{name: "calculator", output: "4"}
	te := newTestableEngine(
		[]mockLLMCall{
			{toolCalls: []llms.ToolCall{makeTc("c1", "calculator", "1")}},
			{toolCalls: []llms.ToolCall{makeTc("c2", "calculator", "2")}},
			{content: "forced final"},
		},
		calc,
	)
	te.maxIter = 2

	cfg := &AgentConfig{Tools: []string{"calculator"}}
	ch := te.processStream(context.Background(), "s1", "loop", cfg, 1)
	events := collectEvents(ch)

	types := findEventTypes(events)
	if !containsStr(types, "done") {
		t.Error("missing done event after maxIter")
	}

	found := false
	for _, e := range events {
		if e.Type == "thinking" && strings.Contains(e.Content, "max iterations") {
			found = true
		}
	}
	if !found {
		t.Error("missing 'max iterations reached' thinking event")
	}
}

func TestProcessStream_ToolNotFound(t *testing.T) {
	te := newTestableEngine(
		[]mockLLMCall{
			{toolCalls: []llms.ToolCall{makeTc("c1", "nonexistent", "go")}},
			{content: "Tool not found"},
		},
	)

	cfg := &AgentConfig{Tools: []string{"nonexistent"}}
	ch := te.processStream(context.Background(), "s1", "missing tool", cfg, 1)
	events := collectEvents(ch)

	found := false
	for _, e := range events {
		if e.Type == "tool_call" && !e.Success {
			found = true
		}
	}
	if !found {
		t.Error("expected failed tool_call for unknown tool")
	}
}

func TestProcessStream_ContextCancel(t *testing.T) {
	slow := &mockTool{name: "slow", output: "late", delay: 10 * time.Second}
	te := newTestableEngine(
		[]mockLLMCall{
			{toolCalls: []llms.ToolCall{makeTc("c1", "slow", "go")}},
		},
		slow,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	cfg := &AgentConfig{Tools: []string{"slow"}}
	ch := te.processStream(ctx, "s1", "cancel me", cfg, 1)
	events := collectEvents(ch)

	hasFailure := false
	for _, e := range events {
		if e.Type == "tool_call" && !e.Success {
			hasFailure = true
		}
	}
	if !hasFailure {
		t.Error("expected failed tool after context cancel")
	}
}
