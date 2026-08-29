package kb

import (
	"context"
	"fmt"

	"ansmeee-ai-agent/internal/config"
	"ansmeee-ai-agent/pkg/logger"

	"go.uber.org/zap"
)

// Vector backend names for KB stores. Keep in sync with config KBConfig.VectorBackend.
const (
	VectorBackendMemory = "memory"
	VectorBackendMilvus = "milvus"
	VectorBackendRedis  = "redis"
)

// NewVectorStoreByConfig creates a VectorStore based on KB config.
// Unsupported / unavailable backends degrade to in-memory store with a warning,
// so the KB system never blocks start-up.
func NewVectorStoreByConfig(cfg config.KBConfig) VectorStore {
	switch normalizeBackend(cfg.VectorBackend) {
	case VectorBackendMilvus:
		store, err := NewMilvusVectorStore(cfg)
		if err != nil {
			logger.L().Warn("milvus vector backend unavailable, falling back to memory",
				zap.Error(err))
			return NewInMemoryVectorStore()
		}
		logger.L().Info("kb using milvus vector backend",
			zap.String("address", milvusAddress(cfg)))
		return store
	case VectorBackendRedis:
		store, err := NewRedisVectorStore(cfg)
		if err != nil {
			logger.L().Warn("redis vector backend unavailable, falling back to memory",
				zap.Error(err))
			return NewInMemoryVectorStore()
		}
		logger.L().Info("kb using redis vector backend",
			zap.String("addr", redisAddress(cfg)))
		return store
	case VectorBackendMemory, "":
		return NewInMemoryVectorStore()
	default:
		logger.L().Warn("unknown kb vector backend, using in-memory",
			zap.String("backend", cfg.VectorBackend))
		return NewInMemoryVectorStore()
	}
}

func normalizeBackend(b string) string {
	switch b {
	case "", "none", "noop":
		return VectorBackendMemory
	default:
		return b
	}
}

func milvusAddress(cfg config.KBConfig) string {
	m := cfg.Milvus
	if m.Address != "" {
		return m.Address
	}
	return ""
}

func redisAddress(cfg config.KBConfig) string {
	r := cfg.Redis
	if r.Addr != "" {
		return r.Addr
	}
	return ""
}

// ---- helpers exported for test-backend construction ----

// MilvusVectorStore is a placeholder that degrades to in-memory until the
// Milvus SDK is wired in. It satisfies the VectorStore interface so callers
// can safely rely on the type being non-nil.
type MilvusVectorStore struct {
	fallback VectorStore
	addr     string
}

// NewMilvusVectorStore constructs the Milvus backend. When the SDK is
// unavailable (the current state) it returns a wrapped in-memory fallback.
// The error return lets the caller decide whether to degrade further; for KB
// use we intentionally keep store non-nil on the fallback path and only
// return an error for truly fatal / misconfigured cases.
func NewMilvusVectorStore(cfg config.KBConfig) (VectorStore, error) {
	// Phase 2a: Milvus SDK not integrated yet. Degrade to in-memory.
	addr := milvusAddress(cfg)
	if addr == "" {
		return nil, fmt.Errorf("milvus address not configured (kb.milvus.address or top-level milvus.address)")
	}
	// Ping / sanity would go here when SDK is wired.
	logger.L().Warn("kb milvus backend is not yet integrated with SDK; using in-memory fallback",
		zap.String("addr", addr))
	return &MilvusVectorStore{
		fallback: NewInMemoryVectorStore(),
		addr:     addr,
	}, nil
}

func (s *MilvusVectorStore) Upsert(ctx context.Context, agentID string, items []VectorItem) error {
	return s.fallback.Upsert(ctx, agentID, items)
}
func (s *MilvusVectorStore) Search(ctx context.Context, agentID string, qVec []float32, topK int, minSim float64) ([]VectorHit, error) {
	return s.fallback.Search(ctx, agentID, qVec, topK, minSim)
}
func (s *MilvusVectorStore) DeleteByIDs(ctx context.Context, agentID string, ids []string) error {
	return s.fallback.DeleteByIDs(ctx, agentID, ids)
}
func (s *MilvusVectorStore) Close() error { return s.fallback.Close() }
