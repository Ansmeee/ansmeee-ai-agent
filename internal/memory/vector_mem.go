package memory

import (
	"context"
	"sort"
	"sync"

	"ansmeee-ai-agent/internal/config"
	"ansmeee-ai-agent/pkg/logger"

	"go.uber.org/zap"
)

// Vector backends.
const (
	VectorBackendMemory = "memory"
	VectorBackendMilvus = "milvus"
)

// maxVectorsPerUser caps per-user semantic items; oldest is evicted at the cap.
const maxVectorsPerUser = 2000

// NewVectorStore selects a vector backend per config. milvus is not yet
// implemented and degrades to the in-memory store with a warning (mirrors the
// Redis→InMemory session-store fallback).
func NewVectorStore(cfg config.VectorConfig) VectorStore {
	switch cfg.Backend {
	case VectorBackendMilvus:
		logger.L().Warn("milvus vector backend not implemented, using in-memory vector store")
		return newMemVectorStore()
	case VectorBackendMemory, "":
		return newMemVectorStore()
	default:
		logger.L().Warn("unknown vector backend, using in-memory vector store",
			zap.String("backend", cfg.Backend))
		return newMemVectorStore()
	}
}

// memVectorStore is a per-user in-memory VectorStore with cosine Top-K search.
type memVectorStore struct {
	mu   sync.RWMutex
	data map[int64][]VectorItem
}

func newMemVectorStore() *memVectorStore {
	return &memVectorStore{data: make(map[int64][]VectorItem)}
}

func (s *memVectorStore) Upsert(_ context.Context, item VectorItem) error {
	if len(item.Embedding) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.data[item.UserID]
	// dedup by MemoryID (>0) so re-admitting the same entry updates in place
	if item.MemoryID != 0 {
		for i := range items {
			if items[i].MemoryID == item.MemoryID {
				items[i] = item
				s.data[item.UserID] = items
				return nil
			}
		}
	}
	items = append(items, item)
	if len(items) > maxVectorsPerUser {
		items = items[len(items)-maxVectorsPerUser:]
	}
	s.data[item.UserID] = items
	return nil
}

func (s *memVectorStore) Search(_ context.Context, userID int64, emb []float32, topK int, minSim float64) ([]ScoredVector, error) {
	if len(emb) == 0 {
		return nil, nil
	}
	s.mu.RLock()
	items := s.data[userID]
	scored := make([]ScoredVector, 0, len(items))
	for _, it := range items {
		sim := cosine(emb, it.Embedding)
		if sim >= minSim {
			scored = append(scored, ScoredVector{Item: it, Score: sim})
		}
	}
	s.mu.RUnlock()

	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if topK > 0 && len(scored) > topK {
		scored = scored[:topK]
	}
	return scored, nil
}

func (s *memVectorStore) Close() error { return nil }
