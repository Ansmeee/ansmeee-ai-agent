package memory

import (
	"context"
	"sync"
	"time"

	"ansmeee-ai-agent/internal/config"
	"ansmeee-ai-agent/pkg/logger"

	"github.com/redis/go-redis/v9"
)

// TaskStore persists L1 TaskState across turns of a session.
type TaskStore interface {
	// Load returns the stored state, or an empty state on miss (never errors on miss).
	Load(ctx context.Context, sessionID string) *TaskState
	Save(ctx context.Context, sessionID string, ts *TaskState) error
	Delete(ctx context.Context, sessionID string) error
}

// NewTaskStore builds the task store per config. Phase 1 supports the in-memory
// backend only; the redis backend is Phase 2 (falls back to memory with a warning).
func NewTaskStore(memCfg *config.MemoryConfig, redisClient *redis.Client) TaskStore {
	if memCfg.Task.Backend == "redis" {
		logger.L().Warn("redis task backend not implemented in Phase 1, using in-memory task store")
	}
	_ = redisClient // reserved for the Phase 2 redis backend
	return newMemTaskStore(memCfg.TTL)
}

type taskEntry struct {
	state     *TaskState
	expiresAt time.Time
}

// memTaskStore is a single-instance in-memory TaskStore with TTL expiry.
type memTaskStore struct {
	mu   sync.RWMutex
	data map[string]*taskEntry
	ttl  time.Duration
}

func newMemTaskStore(ttl time.Duration) *memTaskStore {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	s := &memTaskStore{data: make(map[string]*taskEntry), ttl: ttl}
	go s.cleanup()
	return s
}

func (s *memTaskStore) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for id, e := range s.data {
			if now.After(e.expiresAt) {
				delete(s.data, id)
			}
		}
		s.mu.Unlock()
	}
}

func (s *memTaskStore) Load(ctx context.Context, sessionID string) *TaskState {
	s.mu.RLock()
	e, ok := s.data[sessionID]
	s.mu.RUnlock()
	if !ok || time.Now().After(e.expiresAt) {
		return &TaskState{}
	}
	return e.state.Clone()
}

func (s *memTaskStore) Save(ctx context.Context, sessionID string, ts *TaskState) error {
	if ts == nil {
		return nil
	}
	s.mu.Lock()
	s.data[sessionID] = &taskEntry{state: ts.Clone(), expiresAt: time.Now().Add(s.ttl)}
	s.mu.Unlock()
	return nil
}

func (s *memTaskStore) Delete(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	delete(s.data, sessionID)
	s.mu.Unlock()
	return nil
}
