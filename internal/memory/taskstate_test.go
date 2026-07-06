package memory

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestUpdateTaskState_Transitions(t *testing.T) {
	t.Run("first user message sets goal and planning", func(t *testing.T) {
		ts := &TaskState{}
		UpdateTaskState(ts, UserMessageEvent("帮我查北京天气"))
		if ts.Goal != "帮我查北京天气" {
			t.Errorf("goal = %q", ts.Goal)
		}
		if ts.Stage != StagePlanning {
			t.Errorf("stage = %q, want planning", ts.Stage)
		}
	})

	t.Run("second user message does not overwrite goal", func(t *testing.T) {
		ts := &TaskState{Goal: "原目标", Stage: StageExecuting}
		UpdateTaskState(ts, UserMessageEvent("补充信息"))
		if ts.Goal != "原目标" {
			t.Errorf("goal overwritten to %q", ts.Goal)
		}
	})

	t.Run("tool call appends doing step and executing", func(t *testing.T) {
		ts := &TaskState{Stage: StagePlanning}
		UpdateTaskState(ts, ToolCallEvent("weather"))
		if ts.Stage != StageExecuting {
			t.Errorf("stage = %q, want executing", ts.Stage)
		}
		if len(ts.Steps) != 1 || ts.Steps[0].Tool != "weather" || ts.Steps[0].Status != StepDoing {
			t.Errorf("steps = %+v", ts.Steps)
		}
	})

	t.Run("tool result success marks step done and stores slot", func(t *testing.T) {
		ts := &TaskState{Steps: []Step{{Desc: "weather", Status: StepDoing, Tool: "weather"}}}
		UpdateTaskState(ts, ToolResultEvent("weather", "晴 25C", true))
		if ts.Steps[0].Status != StepDone {
			t.Errorf("step status = %q, want done", ts.Steps[0].Status)
		}
		if ts.Slots["weather"] != "晴 25C" {
			t.Errorf("slot = %q", ts.Slots["weather"])
		}
	})

	t.Run("tool result failure marks step failed", func(t *testing.T) {
		ts := &TaskState{Steps: []Step{{Desc: "weather", Status: StepDoing, Tool: "weather"}}}
		UpdateTaskState(ts, ToolResultEvent("weather", "", false))
		if ts.Steps[0].Status != StepFailed {
			t.Errorf("step status = %q, want failed", ts.Steps[0].Status)
		}
	})

	t.Run("final reply without markers sets done", func(t *testing.T) {
		ts := &TaskState{Goal: "g", Stage: StageExecuting}
		UpdateTaskState(ts, FinalReplyEvent("这是最终答案"))
		if ts.Stage != StageDone {
			t.Errorf("stage = %q, want done", ts.Stage)
		}
		if len(ts.Pending) != 0 {
			t.Errorf("pending = %v, want empty", ts.Pending)
		}
	})

	t.Run("final reply with marker sets confirming and pending", func(t *testing.T) {
		ts := &TaskState{Goal: "g", Stage: StageExecuting}
		UpdateTaskState(ts, FinalReplyEvent("好的\n需确认: 是否发送邮件?"))
		if ts.Stage != StageConfirming {
			t.Errorf("stage = %q, want confirming", ts.Stage)
		}
		if len(ts.Pending) != 1 || !strings.Contains(ts.Pending[0], "是否发送邮件") {
			t.Errorf("pending = %v", ts.Pending)
		}
	})

	t.Run("new user message after done resets to planning", func(t *testing.T) {
		ts := &TaskState{Goal: "g", Stage: StageDone}
		UpdateTaskState(ts, UserMessageEvent("下一个问题"))
		if ts.Stage != StagePlanning {
			t.Errorf("stage = %q, want planning", ts.Stage)
		}
	})

	t.Run("nil state is a no-op", func(t *testing.T) {
		UpdateTaskState(nil, UserMessageEvent("x")) // must not panic
	})
}

func TestRenderTaskState(t *testing.T) {
	if got := renderTaskState(nil); got != "" {
		t.Errorf("nil render = %q, want empty", got)
	}
	if got := renderTaskState(&TaskState{}); got != "" {
		t.Errorf("empty render = %q, want empty", got)
	}
	ts := &TaskState{
		Goal:    "查天气",
		Stage:   StageExecuting,
		Steps:   []Step{{Desc: "weather", Status: StepDone, Tool: "weather"}},
		Pending: []string{"需确认: 城市?"},
	}
	got := renderTaskState(ts)
	for _, want := range []string{"## 当前任务", "查天气", "executing", "[done] weather", "需确认: 城市?"} {
		if !strings.Contains(got, want) {
			t.Errorf("render missing %q in:\n%s", want, got)
		}
	}
}

func TestMemTaskStore_LoadSaveDelete(t *testing.T) {
	ctx := context.Background()
	s := newMemTaskStore(time.Minute)

	// Miss returns empty state, not nil.
	if got := s.Load(ctx, "sess"); got == nil || got.Goal != "" {
		t.Fatalf("miss load = %+v, want empty non-nil", got)
	}

	orig := &TaskState{Goal: "g", Stage: StagePlanning, Slots: map[string]string{"k": "v"}}
	if err := s.Save(ctx, "sess", orig); err != nil {
		t.Fatal(err)
	}

	// Load returns a clone: mutating it must not affect the store.
	loaded := s.Load(ctx, "sess")
	if loaded.Goal != "g" || loaded.Slots["k"] != "v" {
		t.Fatalf("loaded = %+v", loaded)
	}
	loaded.Goal = "mutated"
	loaded.Slots["k"] = "changed"
	again := s.Load(ctx, "sess")
	if again.Goal != "g" || again.Slots["k"] != "v" {
		t.Errorf("store mutated via returned clone: %+v", again)
	}

	if err := s.Delete(ctx, "sess"); err != nil {
		t.Fatal(err)
	}
	if got := s.Load(ctx, "sess"); got.Goal != "" {
		t.Errorf("after delete load = %+v, want empty", got)
	}
}

func TestMemTaskStore_TTLExpiry(t *testing.T) {
	ctx := context.Background()
	s := newMemTaskStore(time.Minute)
	s.Save(ctx, "sess", &TaskState{Goal: "g"})

	// Force expiry by rewriting the entry's deadline into the past.
	s.mu.Lock()
	s.data["sess"].expiresAt = time.Now().Add(-time.Second)
	s.mu.Unlock()

	if got := s.Load(ctx, "sess"); got.Goal != "" {
		t.Errorf("expired load = %+v, want empty", got)
	}
}
