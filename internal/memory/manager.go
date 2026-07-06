package memory

import (
	"context"
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
// Phase 1 wires the fact channel and the task store only; policy/vector/summarizer
// are nil and their code paths are skipped.
type MemoryManager struct {
	chat    SessionStore
	task    TaskStore
	fact    FactStore
	router  *QueryRouter
	extract *DeterministicExtractor
	cfg     config.LongTermConfig
	llog    *zap.Logger
}

// NewMemoryManager builds the Phase 1 (fact-only) orchestrator.
func NewMemoryManager(
	chat SessionStore,
	task TaskStore,
	fact FactStore,
	router *QueryRouter,
	extract *DeterministicExtractor,
	cfg config.LongTermConfig,
) *MemoryManager {
	return &MemoryManager{
		chat:    chat,
		task:    task,
		fact:    fact,
		router:  router,
		extract: extract,
		cfg:     cfg,
		llog:    logger.L(),
	}
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
		if _, err := m.fact.Admit(ctx, e, m.cfg.Admission); err != nil {
			m.llog.Warn("fact admit failed",
				zap.String("key", e.KeyName), zap.Error(err))
		}
	}
}

// OnIdle is the silent LLM-summary path. Phase 1 stub (no LLM).
func (m *MemoryManager) OnIdle(ctx context.Context, userID int64, sessionID string) {
	if m == nil {
		return
	}
	m.llog.Debug("OnIdle noop (Phase 1)", zap.String("session_id", sessionID))
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
