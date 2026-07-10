package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	"ansmeee-ai-agent/internal/config"
)

// stubExtractor returns a fixed set of entries regardless of input.
type stubExtractor struct{ entries []MemoryEntry }

func (s *stubExtractor) Extract(sessionID string, delta []Message) []MemoryEntry {
	return s.entries
}

// fakeChat is a minimal SessionStore returning a canned history.
type fakeChat struct{ history []Message }

func (f *fakeChat) AddMessage(ctx context.Context, sessionID string, msg Message, userID int64) error {
	return nil
}
func (f *fakeChat) History(ctx context.Context, sessionID string) ([]Message, error) {
	return f.history, nil
}
func (f *fakeChat) Exists(ctx context.Context, sessionID string) (bool, error) { return true, nil }
func (f *fakeChat) Delete(ctx context.Context, sessionID string) error         { return nil }
func (f *fakeChat) ListSessions(ctx context.Context, userID int64, agentID string) ([]SessionInfo, error) {
	return nil, nil
}
func (f *fakeChat) SetAgent(ctx context.Context, sessionID, agentID string, userID int64) error {
	return nil
}
func (f *fakeChat) GetAgent(ctx context.Context, sessionID string) (string, error) { return "", nil }
func (f *fakeChat) Close() error                                                   { return nil }

// fakeSummaryStore records summaries and marked sessions.
type fakeSummaryStore struct {
	saved  []SessionSummary
	marked []string
}

func (f *fakeSummaryStore) SaveSummary(ctx context.Context, s SessionSummary) error {
	f.saved = append(f.saved, s)
	return nil
}
func (f *fakeSummaryStore) MarkSessionSummarized(ctx context.Context, sessionID string) error {
	f.marked = append(f.marked, sessionID)
	return nil
}

func hybridCfg() config.LongTermConfig {
	c := baseCfg()
	c.Vector = config.VectorConfig{Enabled: true, Backend: "memory", TopK: 5, MinSimilarity: 0.0, EmbeddingDim: 128}
	c.Scoring.Semantic = 1.0
	return c
}

func TestOnTurnEnd_EpisodicGoesToVectorNotFact(t *testing.T) {
	fact := &fakeFactStore{}
	vec := newMemVectorStore()
	emb := NewHashEmbedder(128)
	m := NewMemoryManager(nil, newMemTaskStore(time.Minute), fact, NewQueryRouter(),
		&stubExtractor{entries: []MemoryEntry{
			{Channel: ChannelFact, Kind: KindEpisodic, KeyName: "user.event", Value: "下周去北京出差", Cardinality: CardinalityMulti, Confidence: 0.8},
		}},
		hybridCfg(),
		WithVectorStore(vec), WithEmbedder(emb),
	)

	m.OnTurnEnd(context.Background(), 7, "s1", []Message{{Role: "human", Content: "x", ID: "m1"}})

	if len(fact.admitted) != 0 {
		t.Fatalf("episodic must not hit fact store, got %+v", fact.admitted)
	}
	got, _ := vec.Search(context.Background(), 7, mustEmbed(emb, "北京出差"), 5, 0.0)
	if len(got) == 0 {
		t.Fatalf("episodic entry should be searchable in vector store")
	}
}

func TestRetrieve_SemanticSection(t *testing.T) {
	vec := newMemVectorStore()
	emb := NewHashEmbedder(128)
	vec.Upsert(context.Background(), VectorItem{UserID: 1, Kind: KindEpisodic, Text: "用户上周去了北京出差", Embedding: mustEmbed(emb, "用户上周去了北京出差"), CreatedAt: time.Now()})

	m := NewMemoryManager(nil, newMemTaskStore(time.Minute), &fakeFactStore{}, NewQueryRouter(),
		NewDeterministicExtractor(), hybridCfg(),
		WithVectorStore(vec), WithEmbedder(emb),
	)

	got := m.Retrieve(context.Background(), 1, "", "s1", "我之前去哪出差了", nil)
	if !strings.Contains(got, "## 相关记忆") || !strings.Contains(got, "北京") {
		t.Fatalf("expected semantic section with recalled memory, got %q", got)
	}
}

func TestOnIdle_SummarizesPersistsAndMarks(t *testing.T) {
	fc := &fakeCompleter{resp: `{"summary":"用户询问并预订了去北京的行程","topics":["出行"]}`}
	sum := &fakeSummaryStore{}
	vec := newMemVectorStore()
	emb := NewHashEmbedder(128)
	chat := &fakeChat{history: []Message{{Role: "human", Content: "订去北京的机票"}, {Role: "ai", Content: "已为你预订"}}}

	m := NewMemoryManager(chat, newMemTaskStore(time.Minute), &fakeFactStore{}, NewQueryRouter(),
		NewDeterministicExtractor(), hybridCfg(),
		WithVectorStore(vec), WithEmbedder(emb),
		WithSummarizer(NewSummarizer(fc)), WithSummaryStore(sum),
	)

	ok := m.OnIdle(context.Background(), 5, "sess-1")
	if !ok {
		t.Fatal("OnIdle should report success")
	}
	if len(sum.saved) != 1 || sum.saved[0].UserID != 5 {
		t.Fatalf("summary not persisted: %+v", sum.saved)
	}
	if len(sum.marked) != 1 || sum.marked[0] != "sess-1" {
		t.Fatalf("session not marked summarized: %+v", sum.marked)
	}
	// summary embedded as episodic memory
	got, _ := vec.Search(context.Background(), 5, mustEmbed(emb, "北京 行程"), 5, 0.0)
	if len(got) == 0 {
		t.Fatalf("summary should be embedded into vector store")
	}
}

func mustEmbed(e Embedder, text string) []float32 {
	v, _ := e.Embed(context.Background(), text)
	return v
}
