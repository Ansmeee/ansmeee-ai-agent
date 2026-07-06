package memory

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"time"

	"ansmeee-ai-agent/internal/config"
	"ansmeee-ai-agent/pkg/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Channels.
const (
	ChannelFact   = "fact"
	ChannelPolicy = "policy"
)

// Cardinality.
const (
	CardinalitySingle = "single"
	CardinalityMulti  = "multi"
)

// Entry status.
const (
	StatusActive   = "active"
	StatusArchived = "archived"
)

// EvidenceRef is a traceable pointer back to the L0 message that justified an entry.
type EvidenceRef struct {
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id,omitempty"`
}

// RecallQuery is a read request against the fact channel.
type RecallQuery struct {
	UserID   int64
	AgentID  string
	Kinds    []string
	Keywords []string
	TopK     int
	MinScore float64
}

// ScoredEntry is a recalled entry with its computed score.
type ScoredEntry struct {
	Entry MemoryEntry
	Score float64
}

// EvolveStat reports what an evolution pass changed.
type EvolveStat struct {
	Decayed  int
	Archived int
	Deleted  int
}

// FactStore is the L2 fact channel.
type FactStore interface {
	Admit(ctx context.Context, e MemoryEntry, ac config.AdmissionConfig) (bool, error)
	Recall(ctx context.Context, q RecallQuery) ([]ScoredEntry, error)
	Get(ctx context.Context, userID int64, key string) ([]MemoryEntry, error)
	MarkUsedAsync(ids []int64)
	Evolve(ctx context.Context, userID int64, now time.Time) (EvolveStat, error)
}

// --- pure decision helpers (unit-tested without a DB) ---

// ValueHash is the dedup key component for a value.
func ValueHash(v string) string {
	sum := md5.Sum([]byte(v))
	return hex.EncodeToString(sum[:])
}

// MarshalEvidence renders evidence refs to the stored JSON form.
func MarshalEvidence(refs []EvidenceRef) string {
	if len(refs) == 0 {
		return ""
	}
	b, err := json.Marshal(refs)
	if err != nil {
		return ""
	}
	return string(b)
}

// hasEvidence reports whether the evidence JSON encodes at least one ref.
func hasEvidence(evidenceJSON string) bool {
	s := strings.TrimSpace(evidenceJSON)
	if s == "" || s == "[]" || s == "null" {
		return false
	}
	var refs []EvidenceRef
	if err := json.Unmarshal([]byte(s), &refs); err != nil {
		return false
	}
	return len(refs) > 0
}

// passesGate is admission step 1: evidence present AND confidence >= threshold.
func passesGate(e MemoryEntry, ac config.AdmissionConfig) bool {
	return hasEvidence(e.Evidence) && e.Confidence >= ac.WriteThreshold
}

type admitAction int

const (
	actionInsert admitAction = iota
	actionSkip
	actionArchiveThenInsert
)

// decideAdmit is admission step 2 (dedup + cardinality merge), given the set of
// existing ACTIVE entries sharing (user, agent, key_name).
func decideAdmit(incoming MemoryEntry, existing []MemoryEntry) (admitAction, []int64) {
	for _, e := range existing {
		if e.ValueHash == incoming.ValueHash {
			return actionSkip, nil // exact value already present → idempotent
		}
	}
	if incoming.Cardinality == CardinalitySingle {
		if len(existing) > 0 {
			ids := make([]int64, 0, len(existing))
			for _, e := range existing {
				ids = append(ids, e.ID)
			}
			return actionArchiveThenInsert, ids
		}
		return actionInsert, nil
	}
	return actionInsert, nil // multi-valued → coexist
}

// freshnessOf is decay^days_since(last_used_at ?? created_at).
func freshnessOf(e MemoryEntry, decay float64, now time.Time) float64 {
	ref := e.CreatedAt
	if e.LastUsedAt != nil {
		ref = *e.LastUsedAt
	}
	days := now.Sub(ref).Hours() / 24
	if days < 0 {
		days = 0
	}
	if decay <= 0 || decay > 1 {
		decay = 0.95
	}
	return math.Pow(decay, days)
}

// relevanceOf: exact key hit = 1.0, kind/keyword partial hit = 0.5, else 0.
func relevanceOf(e MemoryEntry, q RecallQuery) float64 {
	for _, kw := range q.Keywords {
		if kw != "" && strings.EqualFold(e.KeyName, kw) {
			return 1.0
		}
	}
	for _, kw := range q.Keywords {
		if kw == "" {
			continue
		}
		if strings.Contains(strings.ToLower(e.KeyName), strings.ToLower(kw)) ||
			strings.Contains(strings.ToLower(e.Value), strings.ToLower(kw)) {
			return 0.5
		}
	}
	for _, k := range q.Kinds {
		if e.Kind == k {
			return 0.5
		}
	}
	return 0
}

// scoreOf combines the weighted signals.
func scoreOf(e MemoryEntry, q RecallQuery, w config.ScoreWeights, decay float64, now time.Time) float64 {
	return w.Confidence*e.Confidence +
		w.Freshness*freshnessOf(e, decay, now) +
		w.Relevance*relevanceOf(e, q)
}

// --- gorm-backed store ---

type gormFactStore struct {
	db      *gorm.DB
	weights config.ScoreWeights
	decay   float64
	markCh  chan int64
}

// NewFactStore builds the fact channel store, migrating the L2 tables.
func NewFactStore(db *gorm.DB, weights config.ScoreWeights, decay float64) (*gormFactStore, error) {
	if err := db.AutoMigrate(&MemoryEntry{}, &SessionSummary{}); err != nil {
		return nil, err
	}
	s := &gormFactStore{db: db, weights: weights, decay: decay, markCh: make(chan int64, 256)}
	go s.markLoop()
	return s, nil
}

// markLoop batches hit-count/last-used updates off the read path.
func (s *gormFactStore) markLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	pending := make(map[int64]struct{})
	flush := func() {
		if len(pending) == 0 {
			return
		}
		ids := make([]int64, 0, len(pending))
		for id := range pending {
			ids = append(ids, id)
		}
		now := time.Now()
		if err := s.db.Model(&MemoryEntry{}).Where("id IN ?", ids).
			Updates(map[string]any{"last_used_at": now, "hit_count": gorm.Expr("hit_count + 1")}).Error; err != nil {
			logger.L().Warn("fact mark-used flush failed", zap.Error(err))
		}
		pending = make(map[int64]struct{})
	}
	for {
		select {
		case id, ok := <-s.markCh:
			if !ok {
				flush()
				return
			}
			pending[id] = struct{}{}
			if len(pending) >= 128 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (s *gormFactStore) MarkUsedAsync(ids []int64) {
	for _, id := range ids {
		select {
		case s.markCh <- id:
		default: // channel full → drop; read path must never block
		}
	}
}

func (s *gormFactStore) Admit(ctx context.Context, e MemoryEntry, ac config.AdmissionConfig) (bool, error) {
	if !passesGate(e, ac) {
		return false, nil
	}
	if e.Channel == "" {
		e.Channel = ChannelFact
	}
	if e.Cardinality == "" {
		e.Cardinality = CardinalityMulti
	}
	if e.Status == "" {
		e.Status = StatusActive
	}
	if e.ValueHash == "" {
		e.ValueHash = ValueHash(e.Value)
	}

	var existing []MemoryEntry
	if err := s.db.WithContext(ctx).
		Where("user_id = ? AND agent_id = ? AND key_name = ? AND status = ?",
			e.UserID, e.AgentID, e.KeyName, StatusActive).
		Find(&existing).Error; err != nil {
		return false, err
	}

	action, archiveIDs := decideAdmit(e, existing)
	if action == actionSkip {
		return false, nil
	}

	if err := s.enforceQuota(ctx, e.UserID, ac.MaxEntriesPerUser); err != nil {
		logger.L().Warn("fact quota enforcement failed", zap.Error(err))
	}

	return true, s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if action == actionArchiveThenInsert && len(archiveIDs) > 0 {
			if err := tx.Model(&MemoryEntry{}).Where("id IN ?", archiveIDs).
				Update("status", StatusArchived).Error; err != nil {
				return err
			}
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "user_id"}, {Name: "agent_id"}, {Name: "key_name"}, {Name: "value_hash"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"value", "confidence", "evidence", "status", "source", "cardinality", "updated_at",
			}),
		}).Create(&e).Error
	})
}

// enforceQuota deletes the lowest-value active entry when the user is at cap.
func (s *gormFactStore) enforceQuota(ctx context.Context, userID int64, maxEntries int) error {
	if maxEntries <= 0 {
		return nil
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&MemoryEntry{}).
		Where("user_id = ? AND status = ?", userID, StatusActive).Count(&count).Error; err != nil {
		return err
	}
	if count < int64(maxEntries) {
		return nil
	}
	var victim MemoryEntry
	if err := s.db.WithContext(ctx).
		Where("user_id = ? AND status = ?", userID, StatusActive).
		Order("confidence ASC, COALESCE(last_used_at, created_at) ASC").
		First(&victim).Error; err != nil {
		return err
	}
	return s.db.WithContext(ctx).Delete(&MemoryEntry{}, victim.ID).Error
}

func (s *gormFactStore) Recall(ctx context.Context, q RecallQuery) ([]ScoredEntry, error) {
	db := s.db.WithContext(ctx).Model(&MemoryEntry{}).
		Where("user_id = ? AND agent_id = ? AND channel = ? AND status = ?",
			q.UserID, q.AgentID, ChannelFact, StatusActive)
	if len(q.Kinds) > 0 {
		db = db.Where("kind IN ?", q.Kinds)
	}
	var rows []MemoryEntry
	if err := db.Find(&rows).Error; err != nil {
		return nil, err
	}

	now := time.Now()
	scored := make([]ScoredEntry, 0, len(rows))
	for _, e := range rows {
		sc := scoreOf(e, q, s.weights, s.decay, now)
		if sc >= q.MinScore {
			scored = append(scored, ScoredEntry{Entry: e, Score: sc})
		}
	}
	sortScoredDesc(scored)
	if q.TopK > 0 && len(scored) > q.TopK {
		scored = scored[:q.TopK]
	}

	ids := make([]int64, len(scored))
	for i, se := range scored {
		ids[i] = se.Entry.ID
	}
	s.MarkUsedAsync(ids)
	return scored, nil
}

func (s *gormFactStore) Get(ctx context.Context, userID int64, key string) ([]MemoryEntry, error) {
	var rows []MemoryEntry
	err := s.db.WithContext(ctx).
		Where("user_id = ? AND key_name = ? AND status = ?", userID, key, StatusActive).
		Find(&rows).Error
	return rows, err
}

// Evolve applies confidence decay and hard-deletes forgotten/expired entries.
func (s *gormFactStore) Evolve(ctx context.Context, userID int64, now time.Time) (EvolveStat, error) {
	var stat EvolveStat
	res := s.db.WithContext(ctx).
		Where("user_id = ? AND (confidence < ? OR (expires_at IS NOT NULL AND expires_at < ?))",
			userID, 0.1, now).
		Delete(&MemoryEntry{})
	if res.Error != nil {
		return stat, res.Error
	}
	stat.Deleted = int(res.RowsAffected)
	return stat, nil
}

func sortScoredDesc(s []ScoredEntry) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].Score > s[j-1].Score; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
