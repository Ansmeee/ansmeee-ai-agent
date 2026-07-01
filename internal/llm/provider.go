package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"ansmeee-ai-agent/internal/config"
	internaltool "ansmeee-ai-agent/internal/tool"
	"ansmeee-ai-agent/internal/tracing"
	"ansmeee-ai-agent/pkg/logger"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
	"github.com/tmc/langchaingo/tools"
)

// Provider wraps a langchaingo LLM model.
type Provider struct {
	model llms.Model
	cfg   *config.LLMConfig
}

// New creates a new LLM provider from configuration.
func New(cfg *config.LLMConfig) (*Provider, error) {
	return NewWithOverride(cfg, "", "", "")
}

// NewWithOverride creates an LLM provider with optional model/base_url/token overrides.
func NewWithOverride(cfg *config.LLMConfig, model, baseURL, token string) (*Provider, error) {
	if model == "" {
		model = cfg.Model
	}
	if baseURL == "" {
		baseURL = cfg.BaseURL
	}
	if token == "" {
		token = cfg.APIKey
	}

	llmModel, err := openai.New(
		openai.WithToken(token),
		openai.WithModel(model),
		openai.WithBaseURL(baseURL),
	)
	if err != nil {
		return nil, fmt.Errorf("create LLM model: %w", err)
	}

	return &Provider{model: llmModel, cfg: cfg}, nil
}

// ChatResult is the full result of a chat call.
type ChatResult struct {
	Content   string
	ToolCalls []llms.ToolCall
}

// ChatOption allows callers to override model parameters per call.
type ChatOption func(*chatOptions)

type chatOptions struct {
	temperature *float64
	maxTokens   *int
	topP        *float64
}

// WithTemperature overrides the temperature for a single Chat call.
func WithTemperature(t float64) ChatOption {
	return func(o *chatOptions) { o.temperature = &t }
}

// WithChatMaxTokens overrides max tokens for a single Chat call.
func WithChatMaxTokens(n int) ChatOption {
	return func(o *chatOptions) { o.maxTokens = &n }
}

// WithTopP overrides top_p for a single Chat call.
func WithTopP(p float64) ChatOption {
	return func(o *chatOptions) { o.topP = &p }
}

// Chat sends messages and returns the full result including any tool calls.
func (p *Provider) Chat(ctx context.Context, messages []MessageContent, toolList []tools.Tool, opts ...ChatOption) (*ChatResult, error) {
	co := &chatOptions{}
	for _, o := range opts {
		o(co)
	}

	temp := p.cfg.Temperature
	if co.temperature != nil {
		temp = *co.temperature
	}
	maxTk := p.cfg.MaxTokens
	if co.maxTokens != nil {
		maxTk = *co.maxTokens
	}

	msgs := toLLMMessages(messages)
	callOpts := []llms.CallOption{
		llms.WithTemperature(temp),
		llms.WithMaxTokens(maxTk),
	}
	if co.topP != nil {
		callOpts = append(callOpts, llms.WithTopP(*co.topP))
	}
	if len(toolList) > 0 {
		callOpts = append(callOpts, llms.WithTools(toLLMTools(toolList)))
	}

	resp, err := p.model.GenerateContent(ctx, msgs, callOpts...)
	if err != nil {
		logger.L().Error("generate content", tracing.ErrFields(ctx, err)...)
		return nil, fmt.Errorf("generate content: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned from LLM")
	}

	choice := resp.Choices[0]
	return &ChatResult{
		Content:   choice.Content,
		ToolCalls: choice.ToolCalls,
	}, nil
}

// MessageContent is an internal message type.
type MessageContent struct {
	Role         Role
	Content      string
	ToolCallID   string
	ToolCallName string
	ToolCalls    []llms.ToolCall
}

// Role represents a message role.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "human"
	RoleAssistant Role = "ai"
	RoleTool      Role = "tool"
)

func toLLMMessages(messages []MessageContent) []llms.MessageContent {
	result := make([]llms.MessageContent, len(messages))
	for i, m := range messages {
		var parts []llms.ContentPart

		if m.ToolCallID != "" {
			// Tool response: only ToolCallResponse part (content is embedded).
			// langchaingo's OpenAI provider requires exactly one part for tool role.
			parts = append(parts, llms.ToolCallResponse{
				ToolCallID: m.ToolCallID,
				Name:       m.ToolCallName,
				Content:    m.Content,
			})
		} else {
			// Normal message: text content + optional tool calls.
			if m.Content != "" {
				parts = append(parts, llms.TextPart(m.Content))
			}
			for _, tc := range m.ToolCalls {
				parts = append(parts, tc)
			}
		}

		result[i] = llms.MessageContent{
			Role:  llms.ChatMessageType(m.Role),
			Parts: parts,
		}
	}
	return result
}

// isToolCallChunk reports whether a streaming chunk is a tool-call delta rather
// than assistant text. langchaingo's OpenAI client marshals tool-call deltas to
// a JSON array and routes them through the same streaming callback as content,
// so we filter them out to avoid leaking them to the client as chunks. We probe
// with a permissive local struct because llms.ToolCall has a strict custom
// UnmarshalJSON that rejects these deltas.
func isToolCallChunk(chunk []byte) bool {
	var probes []struct {
		Type     string          `json:"type"`
		Function json.RawMessage `json:"function"`
	}
	if err := json.Unmarshal(chunk, &probes); err != nil || len(probes) == 0 {
		return false
	}
	for _, p := range probes {
		if p.Type == "function" || len(p.Function) > 0 {
			return true
		}
	}
	return false
}

func toLLMTools(toolList []tools.Tool) []llms.Tool {
	result := make([]llms.Tool, len(toolList))
	for i, t := range toolList {
		fd := &llms.FunctionDefinition{
			Name:        t.Name(),
			Description: t.Description(),
		}
		if ts, ok := t.(internaltool.ToolWithSchema); ok {
			fd.Parameters = ts.Parameters()
		}
		result[i] = llms.Tool{
			Type:     "function",
			Function: fd,
		}
	}
	return result
}

// ChatStream is like Chat but streams content chunks via onChunk during generation.
// The final ChatResult is still returned with complete Content and ToolCalls.
func (p *Provider) ChatStream(ctx context.Context, messages []MessageContent, toolList []tools.Tool, onChunk func(chunk []byte), opts ...ChatOption) (*ChatResult, error) {
	co := &chatOptions{}
	for _, o := range opts {
		o(co)
	}

	temp := p.cfg.Temperature
	if co.temperature != nil {
		temp = *co.temperature
	}
	maxTk := p.cfg.MaxTokens
	if co.maxTokens != nil {
		maxTk = *co.maxTokens
	}

	msgs := toLLMMessages(messages)
	callOpts := []llms.CallOption{
		llms.WithTemperature(temp),
		llms.WithMaxTokens(maxTk),
		llms.WithStreamingFunc(func(ctx context.Context, chunk []byte) error {
			if onChunk != nil && len(chunk) > 0 && !isToolCallChunk(chunk) {
				onChunk(chunk)
			}
			return nil
		}),
	}
	if co.topP != nil {
		callOpts = append(callOpts, llms.WithTopP(*co.topP))
	}
	if len(toolList) > 0 {
		callOpts = append(callOpts, llms.WithTools(toLLMTools(toolList)))
	}

	resp, err := p.model.GenerateContent(ctx, msgs, callOpts...)
	if err != nil {
		logger.L().Error("generate content (stream)", tracing.ErrFields(ctx, err)...)
		return nil, fmt.Errorf("generate content: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned from LLM")
	}

	choice := resp.Choices[0]
	return &ChatResult{
		Content:   choice.Content,
		ToolCalls: choice.ToolCalls,
	}, nil
}

// WithOverride creates a new Provider with the given overrides.
func (p *Provider) WithOverride(model, baseURL, token string) (*Provider, error) {
	return NewWithOverride(p.cfg, model, baseURL, token)
}
