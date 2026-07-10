package memory

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"ansmeee-ai-agent/internal/config"
	"ansmeee-ai-agent/pkg/logger"

	"go.uber.org/zap"
)

// factTimeout bounds a single fact-channel recall on the read path.
const factTimeout = 200 * time.Millisecond

// budgetBaseChars is the nominal enrichment budget (chars) scaled by BudgetRatio.
const budgetBaseChars = 8000

// MemoryManager orchestrates the L1/L2 memory channels behind a single facade.
// The fact channel and task store are always wired; the vector store, embedder,
// and summarizer are optional and their code paths are skipped when nil.
type MemoryManager struct {
	chat       SessionStore
	task       TaskStore
	fact       FactStore
	router     *QueryRouter
	extract    Extractor
	vec        VectorStore
	embed      Embedder
	summarizer *Summarizer
	summaries  SummaryStore
	cfg        config.LongTermConfig
	llog       *zap.Logger
}

// SummaryStore persists idle-session summaries and marks sessions summarized.
// gormFactStore implements it; kept separate from FactStore so existing fakes
// need not change.
type SummaryStore interface {
	SaveSummary(ctx context.Context, s SessionSummary) error
	MarkSessionSummarized(ctx context.Context, sessionID string) error
}

// ManagerOption configures optional MemoryManager dependencies.
type ManagerOption func(*MemoryManager)

// WithVectorStore attaches the semantic vector channel.
func WithVectorStore(vec VectorStore) ManagerOption {
	return func(m *MemoryManager) { m.vec = vec }
}

// WithEmbedder attaches the embedder used for the vector channel.
func WithEmbedder(e Embedder) ManagerOption {
	return func(m *MemoryManager) { m.embed = e }
}

// WithSummarizer attaches the idle-session LLM summarizer.
func WithSummarizer(s *Summarizer) ManagerOption {
	return func(m *MemoryManager) { m.summarizer = s }
}

// WithSummaryStore attaches the persistence backend for idle-session summaries.
func WithSummaryStore(s SummaryStore) ManagerOption {
	return func(m *MemoryManager) { m.summaries = s }
}

// NewMemoryManager builds the L1/L2 orchestrator. The fact channel and task store
// are required positionally; optional channels are supplied via ManagerOptions.
func NewMemoryManager(
	chat SessionStore,
	task TaskStore,
	fact FactStore,
	router *QueryRouter,
	extract Extractor,
	cfg config.LongTermConfig,
	opts ...ManagerOption,
) *MemoryManager {
	m := &MemoryManager{
		chat:    chat,
		task:    task,
		fact:    fact,
		router:  router,
		extract: extract,
		cfg:     cfg,
		llog:    logger.L(),
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Load forwards to the task store (engine holds only the manager). Returns nil
// when no task store is configured.
func (m *MemoryManager) Load(ctx context.Context, sessionID string) *TaskState {
	if m == nil || m.task == nil {
		return nil
	}
	return m.task.Load(ctx, sessionID)
}

// Save forwards to the task store; no-op when the store or state is absent.
func (m *MemoryManager) Save(ctx context.Context, sessionID string, ts *TaskState) {
	if m == nil || m.task == nil || ts == nil {
		return
	}
	if err := m.task.Save(ctx, sessionID, ts); err != nil {
		m.llog.Warn("task state save failed", zap.String("session_id", sessionID), zap.Error(err))
	}
}

// Retrieve builds the system-prompt enrichment segment: recalled facts + the
// current task state. Read path is bounded and never blocks on a slow channel.
func (m *MemoryManager) Retrieve(ctx context.Context, userID int64, agentID, sessionID, userMsg string, ts *TaskState) string {
	if m == nil || !m.cfg.Enabled {
		return ""
	}

	var sections []string

	if facts := m.retrieveFacts(ctx, userID, agentID, userMsg); facts != "" {
		sections = append(sections, facts)
	}
	if sem := m.retrieveSemantic(ctx, userID, userMsg); sem != "" {
		sections = append(sections, sem)
	}
	if task := renderTaskState(ts); task != "" {
		sections = append(sections, task)
	}

	return strings.Join(sections, "\n\n")
}

func (m *MemoryManager) retrieveFacts(ctx context.Context, userID int64, agentID, userMsg string) string {
	if m.fact == nil || !m.cfg.Fact.Enabled {
		return ""
	}

	var keywords []string
	if m.router != nil && m.cfg.Router {
		feats := m.router.Route(userMsg)
		if feats.Class != ClassFact {
			return ""
		}
		keywords = feats.Keywords
	}

	scored := m.recallFacts(ctx, userID, agentID, keywords)
	if len(scored) == 0 {
		return ""
	}

	lines := make([]string, 0, len(scored))
	for _, se := range scored {
		lines = append(lines, "- "+se.Entry.KeyName+": "+se.Entry.Value)
	}
	lines = truncateToBudget(lines, int(m.cfg.BudgetRatio*budgetBaseChars))
	if len(lines) == 0 {
		return ""
	}
	return "## 用户信息\n" + strings.Join(lines, "\n")
}

func (m *MemoryManager) recallFacts(ctx context.Context, userID int64, agentID string, keywords []string) []ScoredEntry {
	cctx, cancel := context.WithTimeout(ctx, factTimeout)
	defer cancel()

	type result struct {
		entries []ScoredEntry
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		e, err := m.fact.Recall(cctx, RecallQuery{
			UserID:   userID,
			AgentID:  agentID,
			Keywords: keywords,
			TopK:     m.cfg.Fact.TopK,
			MinScore: m.cfg.Fact.MinScore,
		})
		ch <- result{entries: e, err: err}
	}()

	select {
	case <-cctx.Done():
		m.llog.Warn("fact recall timed out", zap.Error(cctx.Err()))
		return nil
	case r := <-ch:
		if r.err != nil {
			m.llog.Warn("fact recall failed", zap.Error(r.err))
			return nil
		}
		return r.entries
	}
}

// OnTurnEnd is the deterministic write path (async, per turn, zero LLM).
func (m *MemoryManager) OnTurnEnd(ctx context.Context, userID int64, sessionID string, delta []Message) {
	if m == nil || m.fact == nil || m.extract == nil || !m.cfg.Enabled {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			m.llog.Error("OnTurnEnd panic recovered", zap.Any("recover", r))
		}
	}()

	for _, e := range m.extract.Extract(sessionID, delta) {
		e.UserID = userID // AgentID stays "" → user-level memory (MVP)
		if e.Kind == KindEpisodic {
			m.admitEpisodic(ctx, e)
			continue
		}
		if _, err := m.fact.Admit(ctx, e, m.cfg.Admission); err != nil {
			m.llog.Warn("fact admit failed",
				zap.String("key", e.KeyName), zap.Error(err))
		}
	}
}

// admitEpisodic stores an episodic entry in the semantic vector channel instead
// of the structured fact table. No-op when the vector channel is not configured.
func (m *MemoryManager) admitEpisodic(ctx context.Context, e MemoryEntry) {
	if m.vec == nil || m.embed == nil || !m.cfg.Vector.Enabled {
		return
	}
	emb, err := m.embed.Embed(ctx, e.Value)
	if err != nil {
		m.llog.Warn("episodic embed failed", zap.Error(err))
		return
	}
	if err := m.vec.Upsert(ctx, VectorItem{
		UserID:    e.UserID,
		Kind:      KindEpisodic,
		Text:      e.Value,
		Embedding: emb,
		CreatedAt: time.Now(),
	}); err != nil {
		m.llog.Warn("episodic upsert failed", zap.Error(err))
	}
}

// OnIdle summarizes an idle session (one LLM call), persists the summary, embeds
// it into the semantic channel as episodic memory, and marks the session done.
// Returns true only when the session was summarized and marked, so the caller can
// avoid re-marking. No-op (returns false) when the summary path is not configured.
func (m *MemoryManager) OnIdle(ctx context.Context, userID int64, sessionID string) bool {
	if m == nil || m.summarizer == nil || m.chat == nil || !m.cfg.Enabled {
		return false
	}
	defer func() {
		if r := recover(); r != nil {
			m.llog.Error("OnIdle panic recovered", zap.Any("recover", r))
		}
	}()

	history, err := m.chat.History(ctx, sessionID)
	if err != nil || len(history) == 0 {
		return false
	}

	res, err := m.summarizer.Summarize(ctx, history)
	if err != nil {
		m.llog.Warn("session summarize failed", zap.String("session_id", sessionID), zap.Error(err))
		return false
	}
	if strings.TrimSpace(res.Summary) == "" {
		return false
	}

	if m.summaries != nil {
		topics, _ := json.Marshal(res.Topics)
		if err := m.summaries.SaveSummary(ctx, SessionSummary{
			SessionID: sessionID,
			UserID:    userID,
			Summary:   res.Summary,
			Topics:    string(topics),
			CreatedAt: time.Now(),
		}); err != nil {
			m.llog.Warn("save summary failed", zap.String("session_id", sessionID), zap.Error(err))
		}
	}

	m.embedSummary(ctx, userID, res.Summary)

	if m.summaries != nil {
		if err := m.summaries.MarkSessionSummarized(ctx, sessionID); err != nil {
			m.llog.Warn("mark summarized failed", zap.String("session_id", sessionID), zap.Error(err))
			return false
		}
	}
	return true
}

// embedSummary stores a session summary in the semantic channel as episodic memory.
func (m *MemoryManager) embedSummary(ctx context.Context, userID int64, summary string) {
	if m.vec == nil || m.embed == nil || !m.cfg.Vector.Enabled {
		return
	}
	emb, err := m.embed.Embed(ctx, summary)
	if err != nil {
		m.llog.Warn("summary embed failed", zap.Error(err))
		return
	}
	if err := m.vec.Upsert(ctx, VectorItem{
		UserID:    userID,
		Kind:      KindEpisodic,
		Text:      summary,
		Embedding: emb,
		CreatedAt: time.Now(),
	}); err != nil {
		m.llog.Warn("summary upsert failed", zap.Error(err))
	}
}

// truncateToBudget keeps a prefix of lines whose cumulative length fits maxChars.
// maxChars <= 0 means no limit.
func truncateToBudget(lines []string, maxChars int) []string {
	if maxChars <= 0 {
		return lines
	}
	total := 0
	for i, l := range lines {
		total += len(l) + 1 // +1 for the joining newline
		if total > maxChars {
			return lines[:i]
		}
	}
	return lines
}
