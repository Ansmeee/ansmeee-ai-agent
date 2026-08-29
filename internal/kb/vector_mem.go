package kb

import (
	"context"
	"math"
	"sort"
	"sync"
)

// inMemoryVectorStore 进程内向量库（Phase1 默认后端，agent_id 命名空间隔离）。
// 暴力余弦相似度扫描，适合开发调试与小规模 KB。
type inMemoryVectorStore struct {
	mu   sync.RWMutex
	data map[string][]vectorEntry // agent_id → entries
}

type vectorEntry struct {
	id   string
	vec  []float32
	meta map[string]any
}

// NewInMemoryVectorStore 创建内存向量库。
func NewInMemoryVectorStore() VectorStore {
	return &inMemoryVectorStore{data: make(map[string][]vectorEntry)}
}

func (s *inMemoryVectorStore) Upsert(_ context.Context, agentID string, items []VectorItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range items {
		entries := s.data[agentID]
		// upsert by id
		found := false
		for i := range entries {
			if entries[i].id == it.ID {
				entries[i].vec = it.Embedding
				entries[i].meta = it.Meta
				found = true
				break
			}
		}
		if !found {
			entries = append(entries, vectorEntry{id: it.ID, vec: it.Embedding, meta: it.Meta})
		}
		s.data[agentID] = entries
	}
	return nil
}

func (s *inMemoryVectorStore) Search(_ context.Context, agentID string, qVec []float32, topK int, minSim float64) ([]VectorHit, error) {
	s.mu.RLock()
	entries := s.data[agentID]
	s.mu.RUnlock()

	type scored struct {
		hit  VectorHit
		score float64
	}
	var results []scored
	for _, e := range entries {
		score := cosineSim(qVec, e.vec)
		if score >= minSim {
			results = append(results, scored{hit: VectorHit{ID: e.id, Score: score}, score: score})
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].score > results[j].score })
	if len(results) > topK {
		results = results[:topK]
	}
	hits := make([]VectorHit, len(results))
	for i, r := range results {
		hits[i] = r.hit
	}
	return hits, nil
}

func (s *inMemoryVectorStore) DeleteByIDs(_ context.Context, agentID string, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := s.data[agentID]
	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}
	filtered := entries[:0]
	for _, e := range entries {
		if _, ok := idSet[e.id]; !ok {
			filtered = append(filtered, e)
		}
	}
	s.data[agentID] = filtered
	return nil
}

func (s *inMemoryVectorStore) Close() error { return nil }

// cosineSim 计算两个等长向量的余弦相似度。
func cosineSim(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
