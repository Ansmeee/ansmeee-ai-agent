package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"ansmeee-ai-agent/internal/llm"
	"ansmeee-ai-agent/internal/memory"
	"ansmeee-ai-agent/internal/models"
	"ansmeee-ai-agent/internal/tool"
	"ansmeee-ai-agent/internal/tracing"
	"ansmeee-ai-agent/pkg/logger"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/tools"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const defaultSystemPrompt = "直接回答用户问题。当需要获取实时信息或进行数学计算时使用工具。评估工具结果后再回复。不要做自我介绍。"

const maxParallelTools = 5

// Engine orchestrates LLM calls with tools and memory.
type Engine struct {
	llm                *llm.Provider
	registry           *tool.Registry
	mem                memory.SessionStore
	callback           *Callback
	systemPrompt       string
	maxIter            int
	toolTimeout        time.Duration
	maxOutputLength    int
	parallelToolCalls  bool
	maxContextMessages int
	memMgr             *memory.MemoryManager
}

// EngineOption configures the engine.
type EngineOption func(*Engine)

func WithSystemPrompt(prompt string) EngineOption {
	return func(e *Engine) { e.systemPrompt = prompt }
}

func WithMaxIter(n int) EngineOption {
	return func(e *Engine) { e.maxIter = n }
}

func WithToolTimeout(d time.Duration) EngineOption {
	return func(e *Engine) { e.toolTimeout = d }
}

func WithMaxOutputLength(n int) EngineOption {
	return func(e *Engine) { e.maxOutputLength = n }
}

func WithParallelToolCalls(b bool) EngineOption {
	return func(e *Engine) { e.parallelToolCalls = b }
}

func WithMaxContextMessages(n int) EngineOption {
	return func(e *Engine) { e.maxContextMessages = n }
}

// WithMemoryManager attaches the L1/L2 memory orchestrator. When nil, all memory
// logic is short-circuited and behavior is identical to the base engine.
func WithMemoryManager(mm *memory.MemoryManager) EngineOption {
	return func(e *Engine) { e.memMgr = mm }
}

// New creates a new Agent engine.
func New(llmProvider *llm.Provider, reg *tool.Registry, mem memory.SessionStore, cb *Callback, opts ...EngineOption) *Engine {
	e := &Engine{
		llm:                llmProvider,
		registry:           reg,
		mem:                mem,
		callback:           cb,
		systemPrompt:       defaultSystemPrompt,
		maxIter:            5,
		toolTimeout:        30 * time.Second,
		maxOutputLength:    4096,
		parallelToolCalls:  true,
		maxContextMessages: 50,
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// AgentConfig holds per-request agent configuration resolved from the Agent model.
type AgentConfig struct {
	Prompt            string
	Tools             []string
	Model             string
	Temperature       *float64
	MaxTokens         int
	TopP              *float64
	MaxIterations     int
	ParallelToolCalls *bool
}

// StreamEvent is emitted during streaming.
type StreamEvent struct {
	Type       string `json:"type"`
	Content    string `json:"content,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	Arguments  string `json:"arguments,omitempty"`
	Result     string `json:"result,omitempty"`
	Success    bool   `json:"success,omitempty"`
	Error      error  `json:"-"`
	Iteration  int    `json:"iteration,omitempty"`
}

// ProcessStream handles a streaming chat turn with the ReAct loop.
func (e *Engine) ProcessStream(ctx context.Context, sessionID, userMessage string, agentCfg *AgentConfig, modelCfg *models.ModelConfig, userID int64) <-chan StreamEvent {
	ch := make(chan StreamEvent, 20)

	go func() {
		defer close(ch)
		defer func() {
			if r := recover(); r != nil {
				ch <- StreamEvent{Type: "error", Error: fmt.Errorf("panic: %v", r)}
			}
		}()

		prompt := e.systemPrompt
		if agentCfg != nil && agentCfg.Prompt != "" {
			prompt = agentCfg.Prompt
		}

		maxIter := e.maxIter
		if agentCfg != nil && agentCfg.MaxIterations > 0 {
			maxIter = agentCfg.MaxIterations
		}

		var task *memory.TaskState
		var turnDelta []memory.Message
		if e.memMgr != nil {
			task = e.memMgr.Load(ctx, sessionID)
		}
		turnDelta = append(turnDelta, memory.Message{Role: models.RoleHuman, Content: userMessage})

		if err := e.mem.AddMessage(ctx, sessionID, memory.Message{Role: models.RoleHuman, Content: userMessage}, userID); err != nil {
			logger.L().Error("save user message failed", tracing.ErrFields(ctx, err)...)
			ch <- StreamEvent{Type: "error", Error: fmt.Errorf("save user message: %w", err)}
			return
		}

		history, err := e.mem.History(ctx, sessionID)
		if err != nil {
			logger.L().Warn("load history failed, starting fresh", tracing.ErrFields(ctx, err)...)
		}

		const memAgentID = "" // MVP: user-level memory (design §5.2 F1)
		if e.memMgr != nil {
			enrich := e.memMgr.Retrieve(ctx, userID, memAgentID, sessionID, userMessage, task)
			prompt = appendEnrichment(prompt, enrich)
			if task != nil {
				memory.UpdateTaskState(task, memory.UserMessageEvent(userMessage))
			}
		}
		messages := e.buildMessages(prompt, history)

		filteredTools := e.resolveTools(agentCfg)
		llmProvider := e.resolveLLMProvider(agentCfg, modelCfg)
		chatOpts := e.buildChatOptions(agentCfg)

		parallelToolCalls := e.parallelToolCalls
		if agentCfg != nil && agentCfg.ParallelToolCalls != nil {
			parallelToolCalls = *agentCfg.ParallelToolCalls
		}

		for iter := 0; iter < maxIter; iter++ {
			ch <- StreamEvent{Type: "thinking", Iteration: iter + 1}
			e.callback.OnLLMStart(ctx, sessionID)

			streamed := false
			result, err := llmProvider.ChatStream(ctx, messages, filteredTools, func(chunk []byte) {
				ch <- StreamEvent{Type: "chunk", Content: string(chunk)}
				streamed = true
			}, chatOpts...)

			if err != nil {
				logger.L().Error("chat stream failed", tracing.ErrFields(ctx, err)...)
				ch <- StreamEvent{Type: "error", Error: err}
				return
			}

			if len(result.ToolCalls) == 0 {
				if !streamed && result.Content != "" {
					ch <- StreamEvent{Type: "chunk", Content: result.Content}
				}
				e.saveMessage(ctx, sessionID, memory.Message{
					Role: models.RoleAI, Content: result.Content,
				}, userID)
				e.callback.OnLLMEnd(ctx, sessionID, 0, 0)
				e.finalizeTask(ctx, task, turnDelta, sessionID, userID, result.Content)
				ch <- StreamEvent{Type: "done", Content: sessionID}
				return
			}

			toolCallMsg := memory.Message{
				Role:    models.RoleFunction,
				Content: buildToolCallJSON(result.ToolCalls),
			}
			e.saveMessage(ctx, sessionID, toolCallMsg, userID)
			messages = append(messages, toolCallToLLMMessage(result))

			if e.memMgr != nil && task != nil {
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
				toolMsgs = e.executeToolsConcurrently(ctx, ch, result.ToolCalls, sessionID, userID)
			} else {
				for _, tc := range result.ToolCalls {
					toolMsgs = append(toolMsgs, e.executeAndEmitTool(ctx, ch, tc, sessionID, userID))
				}
			}
			messages = append(messages, toolMsgs...)

			if e.memMgr != nil && task != nil {
				for _, tm := range toolMsgs {
					success := !strings.HasPrefix(tm.Content, "Error:")
					memory.UpdateTaskState(task, memory.ToolResultEvent(tm.ToolCallName, truncateSummary(tm.Content), success))
				}
			}
			e.callback.OnLLMEnd(ctx, sessionID, 0, 0)
		}

		ch <- StreamEvent{Type: "thinking", Content: "max iterations reached, generating final answer"}
		streamed := false
		result, err := llmProvider.ChatStream(ctx, messages, nil, func(chunk []byte) {
			ch <- StreamEvent{Type: "chunk", Content: string(chunk)}
			streamed = true
		}, chatOpts...)
		if err != nil {
			logger.L().Error("chat stream (final) failed", tracing.ErrFields(ctx, err)...)
			ch <- StreamEvent{Type: "error", Error: err}
			return
		}
		if !streamed && result.Content != "" {
			ch <- StreamEvent{Type: "chunk", Content: result.Content}
		}
		e.saveMessage(ctx, sessionID, memory.Message{
			Role: models.RoleAI, Content: result.Content,
		}, userID)
		e.callback.OnLLMEnd(ctx, sessionID, 0, 0)
		e.finalizeTask(ctx, task, turnDelta, sessionID, userID, result.Content)
		ch <- StreamEvent{Type: "done", Content: sessionID}
	}()

	return ch
}

func (e *Engine) saveMessage(ctx context.Context, sessionID string, msg memory.Message, userID int64) {
	if err := e.mem.AddMessage(ctx, sessionID, msg, userID); err != nil {
		e.callback.Logger.Warn("failed to save message",
			append(tracing.ZapFields(ctx),
				zap.String("session", sessionID),
				zap.String("role", msg.Role),
				zap.Error(err),
			)...,
		)
	}
}

// finalizeTask applies the terminal transition, persists task state, and starts
// the deterministic (zero-LLM) extraction over the accumulated turn delta.
func (e *Engine) finalizeTask(ctx context.Context, task *memory.TaskState, turnDelta []memory.Message, sessionID string, userID int64, finalContent string) {
	if e.memMgr == nil || task == nil {
		return
	}
	memory.UpdateTaskState(task, memory.FinalReplyEvent(finalContent))
	turnDelta = append(turnDelta, memory.Message{Role: models.RoleAI, Content: finalContent})
	e.memMgr.Save(ctx, sessionID, task)
	go e.memMgr.OnTurnEnd(context.WithoutCancel(ctx), userID, sessionID, turnDelta)
}

// appendEnrichment appends the memory enrichment segment below the base prompt.
func appendEnrichment(prompt, enrich string) string {
	if enrich == "" {
		return prompt
	}
	return prompt + "\n\n" + enrich
}

// truncateSummary bounds a tool-result summary stored into TaskState slots.
func truncateSummary(s string) string {
	const maxRunes = 200
	r := []rune(s)
	if len(r) > maxRunes {
		return string(r[:maxRunes])
	}
	return s
}

func (e *Engine) resolveTools(agentCfg *AgentConfig) []tools.Tool {
	if agentCfg == nil || len(agentCfg.Tools) == 0 {
		return nil
	}
	return e.registry.GetByNames(agentCfg.Tools)
}

func (e *Engine) resolveLLMProvider(agentCfg *AgentConfig, userCfg *models.ModelConfig) *llm.Provider {
	model, baseURL, token := "", "", ""

	if userCfg != nil && userCfg.Model != "" {
		model = userCfg.Model
		baseURL = userCfg.BaseURL
		token = userCfg.Token
	}

	if agentCfg != nil && agentCfg.Model != "" {
		model = agentCfg.Model
	}

	if model == "" {
		return e.llm
	}

	if p, err := e.llm.WithOverride(model, baseURL, token); err == nil {
		return p
	} else {
		logger.L().Warn("override LLM provider failed, using default", zap.Error(err))
	}
	return e.llm
}

func (e *Engine) buildChatOptions(agentCfg *AgentConfig) []llm.ChatOption {
	if agentCfg == nil {
		return nil
	}
	var opts []llm.ChatOption
	if agentCfg.Temperature != nil {
		opts = append(opts, llm.WithTemperature(*agentCfg.Temperature))
	}
	if agentCfg.MaxTokens > 0 {
		opts = append(opts, llm.WithChatMaxTokens(agentCfg.MaxTokens))
	}
	if agentCfg.TopP != nil {
		opts = append(opts, llm.WithTopP(*agentCfg.TopP))
	}

	return opts
}

func (e *Engine) executeToolWithTimeout(ctx context.Context, name, input string) (output string, err error) {
	timeout := e.toolTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	maxOutputLen := e.maxOutputLength
	if maxOutputLen == 0 {
		maxOutputLen = 4096
	}

	toolCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tool %q panicked: %v", name, r)
		}
	}()

	traceID := tracing.FromContext(ctx).TraceID
	span := e.callback.OnToolStart(ctx, traceID, name, input)
	output, err = e.registry.Call(toolCtx, name, input)
	e.callback.OnToolEnd(span, name, output)

	if len(output) > maxOutputLen {
		output = output[:maxOutputLen] + "\n...(output truncated at " + strconv.Itoa(maxOutputLen) + " chars, data may be incomplete)"
	}

	return
}

func (e *Engine) executeAndEmitTool(ctx context.Context, ch chan<- StreamEvent, tc llms.ToolCall, sessionID string, userID int64) llm.MessageContent {
	name, args := "", ""
	if tc.FunctionCall != nil {
		name = tc.FunctionCall.Name
		args = tc.FunctionCall.Arguments
	}

	output, err := e.executeToolWithTimeout(ctx, name, args)
	success := err == nil
	resultStr := output
	if !success {
		resultStr = fmt.Sprintf("Error: %v", err)
	}

	ch <- StreamEvent{
		Type: "tool_call", ToolCallID: tc.ID,
		ToolName: name, Arguments: args,
		Result: resultStr, Success: success,
	}

	e.saveMessage(ctx, sessionID, memory.Message{
		Role:    models.RoleTool,
		Content: buildToolResultJSON(tc.ID, name, resultStr),
	}, userID)

	return llm.MessageContent{
		Role: llm.RoleTool, Content: resultStr,
		ToolCallID: tc.ID, ToolCallName: name,
	}
}

func (e *Engine) executeToolsConcurrently(
	ctx context.Context, ch chan<- StreamEvent,
	toolCalls []llms.ToolCall, sessionID string, userID int64,
) []llm.MessageContent {
	type toolResult struct {
		Index   int
		CallID  string
		Name    string
		Args    string
		Output  string
		Success bool
		Message llm.MessageContent
	}

	results := make([]toolResult, len(toolCalls))
	var mu sync.Mutex
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(maxParallelTools)

	for i, tc := range toolCalls {
		name, args := "", ""
		if tc.FunctionCall != nil {
			name = tc.FunctionCall.Name
			args = tc.FunctionCall.Arguments
		}

		g.Go(func() error {
			output, err := e.executeToolWithTimeout(gCtx, name, args)
			success := err == nil
			resultStr := output
			if !success {
				resultStr = fmt.Sprintf("Error: %v", err)
			}

			mu.Lock()
			results[i] = toolResult{
				Index: i, CallID: tc.ID, Name: name,
				Args:   args,
				Output: resultStr, Success: success,
				Message: llm.MessageContent{
					Role: llm.RoleTool, Content: resultStr,
					ToolCallID: tc.ID, ToolCallName: name,
				},
			}
			mu.Unlock()
			return nil
		})
	}
	g.Wait()

	var msgs []llm.MessageContent
	for _, r := range results {
		ch <- StreamEvent{
			Type: "tool_call", ToolCallID: r.CallID,
			ToolName: r.Name, Arguments: r.Args,
			Result: r.Output, Success: r.Success,
		}
		msgs = append(msgs, r.Message)

		e.saveMessage(ctx, sessionID, memory.Message{
			Role:    models.RoleTool,
			Content: buildToolResultJSON(r.CallID, r.Name, r.Output),
		}, userID)
	}
	return msgs
}

func toolCallToLLMMessage(result *llm.ChatResult) llm.MessageContent {
	return llm.MessageContent{
		Role:      llm.RoleAssistant,
		Content:   result.Content,
		ToolCalls: result.ToolCalls,
	}
}

func (e *Engine) buildMessages(systemPrompt string, history []memory.Message) []llm.MessageContent {
	trimmed := trimHistory(history, e.maxContextMessages)

	msgs := []llm.MessageContent{{Role: llm.RoleSystem, Content: systemPrompt}}
	for _, m := range trimmed {
		switch m.Role {
		case models.RoleHuman:
			msgs = append(msgs, llm.MessageContent{Role: llm.RoleUser, Content: m.Content})
		case models.RoleAI:
			msgs = append(msgs, llm.MessageContent{Role: llm.RoleAssistant, Content: m.Content})
		case models.RoleFunction:
			var tc toolCallContent
			if err := json.Unmarshal([]byte(m.Content), &tc); err == nil {
				msg := llm.MessageContent{Role: llm.RoleAssistant}
				for _, call := range tc.ToolCalls {
					msg.ToolCalls = append(msg.ToolCalls, llms.ToolCall{
						ID:   call.ID,
						Type: "function",
						FunctionCall: &llms.FunctionCall{
							Name: call.Name, Arguments: call.Arguments,
						},
					})
				}
				msgs = append(msgs, msg)
			} else {
				logger.L().Warn("unmarshal tool call message failed", zap.Error(err))
			}
		case models.RoleTool:
			var tr toolResultContent
			if err := json.Unmarshal([]byte(m.Content), &tr); err == nil {
				msgs = append(msgs, llm.MessageContent{
					Role: llm.RoleTool, Content: tr.Result,
					ToolCallID: tr.ToolCallID, ToolCallName: tr.Name,
				})
			} else {
				logger.L().Warn("unmarshal tool result message failed", zap.Error(err))
			}
		}
	}
	return msgs
}

// trimHistory keeps the most recent messages while preserving tool_call/tool pairs.
func trimHistory(history []memory.Message, maxMessages int) []memory.Message {
	if maxMessages <= 0 || len(history) <= maxMessages {
		return history
	}

	start := len(history) - maxMessages

	// Ensure we don't split a function/tool pair — if the message at start
	// is a "tool" response, back up to include its preceding function message.
	for start > 0 && history[start].Role == models.RoleTool {
		start--
	}

	return history[start:]
}
