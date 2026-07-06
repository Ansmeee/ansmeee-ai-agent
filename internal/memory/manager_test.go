package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	"ansmeee-ai-agent/internal/config"
)

// fakeFactStore is a controllable FactStore for manager tests.
type fakeFactStore struct {
	recall     []ScoredEntry
	recallErr  error
	recallWait time.Duration
	admitted   []MemoryEntry
}

func (f *fakeFactStore) Admit(ctx context.Context, e MemoryEntry, ac config.AdmissionConfig) (bool, error) {
	f.admitted = append(f.admitted, e)
	return true, nil
}

func (f *fakeFactStore) Recall(ctx context.Context, q RecallQuery) ([]ScoredEntry, error) {
	if f.recallWait > 0 {
		select {
		case <-time.After(f.recallWait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.recall, f.recallErr
}

func (f *fakeFactStore) Get(ctx context.Context, userID int64, key string) ([]MemoryEntry, error) {
	return nil, nil
}
func (f *fakeFactStore) MarkUsedAsync(ids []int64) {}
func (f *fakeFactStore) Evolve(ctx context.Context, userID int64, now time.Time) (EvolveStat, error) {
	return EvolveStat{}, nil
}

func baseCfg() config.LongTermConfig {
	return config.LongTermConfig{
		Enabled:     true,
		Router:      true,
		Fact:        config.ChannelConfig{Enabled: true, TopK: 5, MinScore: 0.5},
		Scoring:     config.ScoreWeights{Confidence: 0.3, Freshness: 0.2, Relevance: 0.5},
		BudgetRatio: 0.2,
		Admission:   config.AdmissionConfig{WriteThreshold: 0.6, MaxEntriesPerUser: 1000},
	}
}

func newTestManager(fact FactStore, cfg config.LongTermConfig) *MemoryManager {
	return NewMemoryManager(nil, newMemTaskStore(time.Minute), fact, NewQueryRouter(), NewDeterministicExtractor(), cfg)
}

func TestRetrieve_FactSection(t *testing.T) {
	fact := &fakeFactStore{recall: []ScoredEntry{
		{Entry: MemoryEntry{KeyName: "user.name", Value: "张三"}, Score: 0.9},
	}}
	m := newTestManager(fact, baseCfg())

	got := m.Retrieve(context.Background(), 1, "", "s1", "我叫什么", nil)
	if !strings.Contains(got, "## 用户信息") || !strings.Contains(got, "user.name: 张三") {
		t.Fatalf("expected fact section, got %q", got)
	}
}

func TestRetrieve_RouterDefaultSkipsFact(t *testing.T) {
	fact := &fakeFactStore{recall: []ScoredEntry{
		{Entry: MemoryEntry{KeyName: "user.name", Value: "张三"}},
	}}
	m := newTestManager(fact, baseCfg())

	// A statement with no question markers routes to ClassDefault → no fact recall.
	got := m.Retrieve(context.Background(), 1, "", "s1", "今天天气不错", nil)
	if strings.Contains(got, "用户信息") {
		t.Fatalf("expected no fact section for default-class query, got %q", got)
	}
}

func TestRetrieve_DisabledShortCircuits(t *testing.T) {
	cfg := baseCfg()
	cfg.Enabled = false
	fact := &fakeFactStore{recall: []ScoredEntry{{Entry: MemoryEntry{KeyName: "k", Value: "v"}}}}
	m := newTestManager(fact, cfg)

	if got := m.Retrieve(context.Background(), 1, "", "s1", "我叫什么", nil); got != "" {
		t.Fatalf("disabled manager must return empty, got %q", got)
	}
}

func TestRetrieve_TimeoutDegrades(t *testing.T) {
	fact := &fakeFactStore{
		recall:     []ScoredEntry{{Entry: MemoryEntry{KeyName: "user.name", Value: "张三"}}},
		recallWait: 2 * factTimeout,
	}
	m := newTestManager(fact, baseCfg())

	got := m.Retrieve(context.Background(), 1, "", "s1", "我叫什么", nil)
	if strings.Contains(got, "用户信息") {
		t.Fatalf("slow fact recall must degrade to empty, got %q", got)
	}
}

func TestRetrieve_TaskSection(t *testing.T) {
	m := newTestManager(&fakeFactStore{}, baseCfg())
	ts := &TaskState{Goal: "订机票", Stage: StageExecuting}

	got := m.Retrieve(context.Background(), 1, "", "s1", "今天天气不错", ts)
	if !strings.Contains(got, "## 当前任务") || !strings.Contains(got, "订机票") {
		t.Fatalf("expected task section, got %q", got)
	}
}

func TestRetrieve_BudgetTruncation(t *testing.T) {
	fact := &fakeFactStore{recall: []ScoredEntry{
		{Entry: MemoryEntry{KeyName: "user.a", Value: strings.Repeat("x", 40)}},
		{Entry: MemoryEntry{KeyName: "user.b", Value: strings.Repeat("y", 40)}},
	}}
	cfg := baseCfg()
	cfg.BudgetRatio = 0.005 // 0.005 * 8000 = 40 chars → only the first line fits
	m := newTestManager(fact, cfg)

	got := m.Retrieve(context.Background(), 1, "", "s1", "我叫什么", nil)
	if strings.Contains(got, "user.b") {
		t.Fatalf("expected budget to drop the second fact, got %q", got)
	}
}

func TestOnTurnEnd_AdmitsExtracted(t *testing.T) {
	fact := &fakeFactStore{}
	m := newTestManager(fact, baseCfg())

	delta := []Message{{Role: "human", Content: "我叫张三", ID: "m1"}}
	m.OnTurnEnd(context.Background(), 42, "s1", delta)

	if len(fact.admitted) == 0 {
		t.Fatal("expected at least one admitted entry")
	}
	found := false
	for _, e := range fact.admitted {
		if e.KeyName == "user.name" && e.Value == "张三" {
			if e.UserID != 42 {
				t.Fatalf("expected UserID stamped to 42, got %d", e.UserID)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("expected user.name=张三 admitted, got %+v", fact.admitted)
	}
}

func TestLoadSave_Forwarding(t *testing.T) {
	m := newTestManager(&fakeFactStore{}, baseCfg())
	ctx := context.Background()

	if ts := m.Load(ctx, "s1"); ts == nil || ts.Goal != "" {
		t.Fatalf("expected empty state on miss, got %+v", ts)
	}
	m.Save(ctx, "s1", &TaskState{Goal: "订机票", Stage: StagePlanning})
	if ts := m.Load(ctx, "s1"); ts == nil || ts.Goal != "订机票" {
		t.Fatalf("expected persisted goal, got %+v", ts)
	}
}
