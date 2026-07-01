package llm

import (
	"testing"

	"github.com/tmc/langchaingo/llms"
)

func TestToLLMMessages_ToolResponse(t *testing.T) {
	msgs := []MessageContent{
		{
			Role:         RoleTool,
			Content:      "4",
			ToolCallID:   "call_1",
			ToolCallName: "calculator",
		},
	}

	result := toLLMMessages(msgs)

	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}

	m := result[0]
	if m.Role != llms.ChatMessageTypeTool {
		t.Errorf("role = %q, want %q", m.Role, llms.ChatMessageTypeTool)
	}

	// langchaingo's OpenAI provider requires exactly one part for tool role.
	if len(m.Parts) != 1 {
		t.Fatalf("expected exactly 1 part for tool response, got %d", len(m.Parts))
	}

	tcr, ok := m.Parts[0].(llms.ToolCallResponse)
	if !ok {
		t.Fatalf("expected ToolCallResponse, got %T", m.Parts[0])
	}
	if tcr.ToolCallID != "call_1" {
		t.Errorf("ToolCallID = %q, want call_1", tcr.ToolCallID)
	}
	if tcr.Name != "calculator" {
		t.Errorf("Name = %q, want calculator", tcr.Name)
	}
	if tcr.Content != "4" {
		t.Errorf("Content = %q, want 4", tcr.Content)
	}
}

func TestToLLMMessages_AssistantWithToolCalls(t *testing.T) {
	tc := llms.ToolCall{
		ID:   "call_1",
		Type: "function",
		FunctionCall: &llms.FunctionCall{
			Name:      "calculator",
			Arguments: `{"expr":"2+2"}`,
		},
	}
	msgs := []MessageContent{
		{
			Role:      RoleAssistant,
			Content:   "Let me calculate",
			ToolCalls: []llms.ToolCall{tc},
		},
	}

	result := toLLMMessages(msgs)

	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}

	m := result[0]
	if m.Role != llms.ChatMessageTypeAI {
		t.Errorf("role = %q, want %q", m.Role, llms.ChatMessageTypeAI)
	}

	// Should have TextContent + ToolCall = 2 parts
	if len(m.Parts) != 2 {
		t.Fatalf("expected 2 parts (text + tool call), got %d", len(m.Parts))
	}

	if _, ok := m.Parts[0].(llms.TextContent); !ok {
		t.Errorf("parts[0] should be TextContent, got %T", m.Parts[0])
	}
	if _, ok := m.Parts[1].(llms.ToolCall); !ok {
		t.Errorf("parts[1] should be ToolCall, got %T", m.Parts[1])
	}
}

func TestToLLMMessages_BasicRoles(t *testing.T) {
	msgs := []MessageContent{
		{Role: RoleSystem, Content: "You are helpful"},
		{Role: RoleUser, Content: "Hello"},
		{Role: RoleAssistant, Content: "Hi there"},
	}

	result := toLLMMessages(msgs)

	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}

	expected := []llms.ChatMessageType{
		llms.ChatMessageTypeSystem,
		llms.ChatMessageTypeHuman,
		llms.ChatMessageTypeAI,
	}
	for i, want := range expected {
		if result[i].Role != want {
			t.Errorf("msgs[%d].Role = %q, want %q", i, result[i].Role, want)
		}
		if len(result[i].Parts) != 1 {
			t.Errorf("msgs[%d] should have 1 part, got %d", i, len(result[i].Parts))
		}
	}
}
