package memory

import (
	"maps"
	"strings"
)

// Task stages.
const (
	StagePlanning   = "planning"
	StageExecuting  = "executing"
	StageConfirming = "confirming"
	StageDone       = "done"
)

// Step statuses.
const (
	StepTodo   = "todo"
	StepDoing  = "doing"
	StepDone   = "done"
	StepFailed = "failed"
)

// pendingMarkers trigger a Pending item + confirming stage when present in a final reply.
var pendingMarkers = []string{"需确认:", "需确认：", "TODO:", "TODO："}

// Step is one ordered unit of work within a task.
type Step struct {
	Desc   string `json:"desc"`
	Status string `json:"status"` // todo | doing | done | failed
	Tool   string `json:"tool,omitempty"`
}

// TaskState is the bounded, deterministic per-session working memory (L1).
type TaskState struct {
	Goal    string            `json:"goal,omitempty"`
	Stage   string            `json:"stage,omitempty"` // planning | executing | confirming | done
	Steps   []Step            `json:"steps,omitempty"`
	Pending []string          `json:"pending,omitempty"`
	Slots   map[string]string `json:"slots,omitempty"`
}

// Clone returns a deep copy so callers can mutate without racing the store.
func (ts *TaskState) Clone() *TaskState {
	if ts == nil {
		return &TaskState{}
	}
	cp := &TaskState{Goal: ts.Goal, Stage: ts.Stage}
	if len(ts.Steps) > 0 {
		cp.Steps = append([]Step(nil), ts.Steps...)
	}
	if len(ts.Pending) > 0 {
		cp.Pending = append([]string(nil), ts.Pending...)
	}
	if len(ts.Slots) > 0 {
		cp.Slots = make(map[string]string, len(ts.Slots))
		maps.Copy(cp.Slots, ts.Slots)
	}
	return cp
}

// TaskEventKind enumerates the deterministic transitions.
type TaskEventKind int

const (
	EventUserMessage TaskEventKind = iota
	EventToolCall
	EventToolResult
	EventFinalReply
)

// TaskEvent drives a single state transition. Zero LLM.
type TaskEvent struct {
	Kind    TaskEventKind
	Content string // user message / final reply text
	Tool    string // tool name for ToolCall / ToolResult
	Success bool   // ToolResult outcome
	Summary string // ToolResult summary → Slots
}

// UserMessageEvent is the first/next user turn.
func UserMessageEvent(content string) TaskEvent {
	return TaskEvent{Kind: EventUserMessage, Content: content}
}

// ToolCallEvent marks a tool invocation.
func ToolCallEvent(tool string) TaskEvent {
	return TaskEvent{Kind: EventToolCall, Tool: tool}
}

// ToolResultEvent marks a tool's completion.
func ToolResultEvent(tool, summary string, success bool) TaskEvent {
	return TaskEvent{Kind: EventToolResult, Tool: tool, Summary: summary, Success: success}
}

// FinalReplyEvent is the model's terminal answer (no tool call).
func FinalReplyEvent(content string) TaskEvent {
	return TaskEvent{Kind: EventFinalReply, Content: content}
}

// UpdateTaskState applies a single event to the state in place. Rule-driven, zero LLM.
// Exported because the agent package drives it from the ReAct loop.
func UpdateTaskState(ts *TaskState, ev TaskEvent) {
	if ts == nil {
		return
	}
	switch ev.Kind {
	case EventUserMessage:
		if ts.Goal == "" {
			ts.Goal = ev.Content
		}
		if ts.Stage == "" || ts.Stage == StageDone {
			ts.Stage = StagePlanning
		}
	case EventToolCall:
		ts.Stage = StageExecuting
		for i := range ts.Steps {
			if ts.Steps[i].Tool == ev.Tool && ts.Steps[i].Status == StepTodo {
				ts.Steps[i].Status = StepDoing
				return
			}
		}
		ts.Steps = append(ts.Steps, Step{Desc: ev.Tool, Status: StepDoing, Tool: ev.Tool})
	case EventToolResult:
		status := StepDone
		if !ev.Success {
			status = StepFailed
		}
		for i := len(ts.Steps) - 1; i >= 0; i-- {
			if ts.Steps[i].Tool == ev.Tool && ts.Steps[i].Status == StepDoing {
				ts.Steps[i].Status = status
				break
			}
		}
		if ev.Summary != "" {
			if ts.Slots == nil {
				ts.Slots = map[string]string{}
			}
			ts.Slots[ev.Tool] = ev.Summary
		}
	case EventFinalReply:
		newPending := scanPending(ev.Content)
		ts.Pending = append(ts.Pending, newPending...)
		if len(newPending) > 0 {
			ts.Stage = StageConfirming
		} else {
			ts.Stage = StageDone
		}
	}
}

// scanPending returns lines of content that carry a pending marker.
func scanPending(content string) []string {
	var out []string
	for line := range strings.SplitSeq(content, "\n") {
		for _, mk := range pendingMarkers {
			if strings.Contains(line, mk) {
				out = append(out, strings.TrimSpace(line))
				break
			}
		}
	}
	return out
}

// renderTaskState renders the "## 当前任务" enrichment section, or "" when empty.
func renderTaskState(ts *TaskState) string {
	if ts == nil || (ts.Goal == "" && len(ts.Steps) == 0) {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 当前任务\n")
	if ts.Goal != "" {
		b.WriteString("目标: ")
		b.WriteString(ts.Goal)
		b.WriteString("\n")
	}
	if ts.Stage != "" {
		b.WriteString("阶段: ")
		b.WriteString(ts.Stage)
		b.WriteString("\n")
	}
	if len(ts.Steps) > 0 {
		b.WriteString("步骤:\n")
		for _, s := range ts.Steps {
			b.WriteString("- [")
			b.WriteString(s.Status)
			b.WriteString("] ")
			b.WriteString(s.Desc)
			b.WriteString("\n")
		}
	}
	if len(ts.Pending) > 0 {
		b.WriteString("待确认:\n")
		for _, p := range ts.Pending {
			b.WriteString("- ")
			b.WriteString(p)
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
