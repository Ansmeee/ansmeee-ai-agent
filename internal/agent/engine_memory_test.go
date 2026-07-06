package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"ansmeee-ai-agent/internal/config"
	"ansmeee-ai-agent/internal/llm"
	"ansmeee-ai-agent/internal/memory"

	"github.com/tmc/langchaingo/tools"
)

// stubFact implements memory.FactStore for engine integration tests.
type stubFact struct {
	mu       sync.Mutex
	recall   []memory.ScoredEntry
	admitted []memory.MemoryEntry
}

func (f *stubFact) Admit(ctx context.Context, e memory.MemoryEntry, ac config.AdmissionConfig) (bool, error) {
	f.mu.Lock()
	f.admitted = append(f.admitted, e)
	f.mu.Unlock()
	return true, nil
}
func (f *stubFact) Recall(ctx context.Context, q memory.RecallQuery) ([]memory.ScoredEntry, error) {
	return f.recall, nil
}
func (f *stubFact) Get(ctx context.Context, userID int64, key string) ([]memory.MemoryEntry, error) {
	return nil, nil
}
func (f *stubFact) MarkUsedAsync(ids []int64) {}
func (f *stubFact) Evolve(ctx context.Context, userID int64, now time.Time) (memory.EvolveStat, error) {
	return memory.EvolveStat{}, nil
}

func newTestMemMgr(fact memory.FactStore) *memory.MemoryManager {
	cfg := config.LongTermConfig{
		Enabled:     true,
		Router:      true,
		Fact:        config.ChannelConfig{Enabled: true, TopK: 5, MinScore: 0},
		BudgetRatio: 1.0,
		Admission:   config.AdmissionConfig{WriteThreshold: 0.6, MaxEntriesPerUser: 1000},
	}
	task := memory.NewTaskStore(&config.MemoryConfig{TTL: time.Minute}, nil)
	return memory.NewMemoryManager(nil, task, fact, memory.NewQueryRouter(), memory.NewDeterministicExtractor(), cfg)
}

// (a) recalled facts are injected into the system message.
func TestProcessStream_Memory_EnrichmentInjected(t *testing.T) {
	fact := &stubFact{recall: []memory.ScoredEntry{
		{Entry: memory.MemoryEntry{KeyName: "user.name", Value: "张三"}, Score: 1},
	}}
	te := newTestableEngine(nil)
	te.Engine.memMgr = newTestMemMgr(fact)

	var captured string
	te.chatFunc = func(_ context.Context, messages []llm.MessageContent, _ []tools.Tool, _ ...llm.ChatOption) (*llm.ChatResult, error) {
		if len(messages) > 0 {
			captured = messages[0].Content
		}
		return &llm.ChatResult{Content: "你好张三"}, nil
	}

	collectEvents(te.processStream(context.Background(), "s1", "我叫什么", nil, 1))

	if !strings.Contains(captured, "## 用户信息") || !strings.Contains(captured, "user.name: 张三") {
		t.Fatalf("system prompt missing enrichment: %q", captured)
	}
}

// (b) task state carries across turns: turn 2 sees turn 1's goal.
func TestProcessStream_Memory_CrossTurnTask(t *testing.T) {
	te := newTestableEngine(nil)
	te.Engine.memMgr = newTestMemMgr(&stubFact{})

	te.chatFunc = func(_ context.Context, _ []llm.MessageContent, _ []tools.Tool, _ ...llm.ChatOption) (*llm.ChatResult, error) {
		return &llm.ChatResult{Content: "已为你规划"}, nil
	}
	collectEvents(te.processStream(context.Background(), "s1", "帮我订机票", nil, 1))

	var captured string
	te.chatFunc = func(_ context.Context, messages []llm.MessageContent, _ []tools.Tool, _ ...llm.ChatOption) (*llm.ChatResult, error) {
		if len(messages) > 0 {
			captured = messages[0].Content
		}
		return &llm.ChatResult{Content: "继续中"}, nil
	}
	collectEvents(te.processStream(context.Background(), "s1", "继续", nil, 1))

	if !strings.Contains(captured, "## 当前任务") || !strings.Contains(captured, "帮我订机票") {
		t.Fatalf("turn 2 system prompt missing carried task: %q", captured)
	}
}

// (d) a no-tool direct answer drives the task to the done stage.
func TestProcessStream_Memory_TaskDone(t *testing.T) {
	te := newTestableEngine(nil)
	mm := newTestMemMgr(&stubFact{})
	te.Engine.memMgr = mm

	te.chatFunc = func(_ context.Context, _ []llm.MessageContent, _ []tools.Tool, _ ...llm.ChatOption) (*llm.ChatResult, error) {
		return &llm.ChatResult{Content: "answer"}, nil
	}
	collectEvents(te.processStream(context.Background(), "s1", "帮我订机票", nil, 1))

	ts := mm.Load(context.Background(), "s1")
	if ts == nil || ts.Stage != memory.StageDone {
		t.Fatalf("expected task stage done, got %+v", ts)
	}
	if ts.Goal != "帮我订机票" {
		t.Fatalf("expected goal carried, got %q", ts.Goal)
	}
}

// (c) helpers behave as no-ops / pure functions.
func TestFinalizeTask_NilMemMgrNoop(t *testing.T) {
	e := &Engine{}                                                               // memMgr nil
	e.finalizeTask(context.Background(), &memory.TaskState{}, nil, "s1", 1, "x") // must not panic
}

func TestAppendEnrichment(t *testing.T) {
	if got := appendEnrichment("base", ""); got != "base" {
		t.Fatalf("empty enrich should return base, got %q", got)
	}
	if got := appendEnrichment("base", "extra"); got != "base\n\nextra" {
		t.Fatalf("got %q", got)
	}
}
