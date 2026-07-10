package memory

import (
	"context"
	"sync"
	"time"

	"ansmeee-ai-agent/internal/config"
	"ansmeee-ai-agent/internal/models"
	"ansmeee-ai-agent/pkg/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// idleScanInterval is how often the scanner sweeps for idle sessions (Phase 1
// uses a fixed ticker; the cron expression in config is honored from Phase 2).
const idleScanInterval = time.Minute

// idleScanBatch caps rows examined per sweep.
const idleScanBatch = 100

// IdleScanner sweeps idle sessions and triggers the silent summary path.
// Phase 1 only invokes MemoryManager.OnIdle (a no-op) — no LLM is called.
type IdleScanner struct {
	db      *gorm.DB
	mem     *MemoryManager
	idleTTL time.Duration
	llog    *zap.Logger

	mu   sync.Mutex
	seen map[string]struct{} // sessions already handled this process lifetime
}

// NewIdleScanner builds the scanner from the memory config.
func NewIdleScanner(db *gorm.DB, mem *MemoryManager, memCfg *config.MemoryConfig) *IdleScanner {
	ttl := memCfg.Task.IdleTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &IdleScanner{
		db:      db,
		mem:     mem,
		idleTTL: ttl,
		llog:    logger.L(),
		seen:    make(map[string]struct{}),
	}
}

// Start runs the sweep loop until ctx is cancelled. Intended to run in a goroutine.
func (s *IdleScanner) Start(ctx context.Context) {
	if s == nil || s.db == nil || s.mem == nil {
		return
	}
	ticker := time.NewTicker(idleScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scanOnce(ctx)
		}
	}
}

func (s *IdleScanner) scanOnce(ctx context.Context) {
	cutoff := time.Now().Add(-s.idleTTL)
	var rows []models.Session
	if err := s.db.WithContext(ctx).
		Where("summarized = ? AND last_active_at IS NOT NULL AND last_active_at < ?", false, cutoff).
		Limit(idleScanBatch).
		Find(&rows).Error; err != nil {
		s.llog.Warn("idle scan query failed", zap.Error(err))
		return
	}

	for _, sess := range rows {
		if s.alreadySeen(sess.UUID) {
			continue
		}
		// OnIdle summarizes the session, persists the summary + episodic embedding,
		// and marks sessions.summarized = 1 on success (no-op when unconfigured).
		s.mem.OnIdle(ctx, sess.UserID, sess.UUID)
	}
}

func (s *IdleScanner) alreadySeen(uuid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[uuid]; ok {
		return true
	}
	s.seen[uuid] = struct{}{}
	return false
}
