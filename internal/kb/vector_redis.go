package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"ansmeee-ai-agent/internal/config"
	"ansmeee-ai-agent/pkg/logger"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// redisKeyspace keeps per-agent vectors in a dedicated namespace so a single
// Redis instance can host many agents without collision.
// Layout:
//   HSET kb:v:<agent_id> <id> <json>   — JSON payload of a vector item
//   DEL  kb:v:<agent_id>              — clear all vectors for an agent (DeleteByIDs uses HDEL)
const (
	redisKeyPrefix = "kb:v:"
)

// redisVectorRecord is the stored JSON payload per chunk id.
type redisVectorRecord struct {
	Vec       []float32      `json:"v"`
	Meta      map[string]any `json:"m,omitempty"`
	CreatedAt int64          `json:"t,omitempty"`
}

// redisVectorStore implements VectorStore on top of a plain Redis instance.
// It HSETs vectors as JSON blobs and runs a brute-force cosine scan at search
// time — acceptable for ~≤10k chunks per agent. Larger deployments should
// switch to the Milvus backend or Redis Stack with RediSearch+VSS.
type redisVectorStore struct {
	rdb *redis.Client
}

// NewRedisVectorStore connects to Redis using the KB-specific config, falling
// back to the top-level Redis config, then to a localhost default. Returns an
// error when the client cannot PING the server so callers can degrade.
func NewRedisVectorStore(cfg config.KBConfig) (VectorStore, error) {
	rc := cfg.Redis
	if rc.Addr == "" {
		return nil, fmt.Errorf("redis addr not configured (kb.redis.addr or top-level redis.addr)")
	}
	opts := &redis.Options{
		Addr:       rc.Addr,
		Password:   rc.Password,
		DB:         rc.DB,
		MaxRetries: rc.MaxRetries,
		PoolSize:   rc.PoolSize,
	}
	if opts.MaxRetries == 0 {
		opts.MaxRetries = 3
	}
	if opts.PoolSize == 0 {
		opts.PoolSize = 10
	}
	rdb := redis.NewClient(opts)

	// Sanity PING to detect an unreachable Redis early; failure is handed back
	// to the factory which degrades to memory.
	ctx, cancel := context.WithTimeout(context.Background(), redisDefaultPingTimeout)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &redisVectorStore{rdb: rdb}, nil
}

func (s *redisVectorStore) Upsert(ctx context.Context, agentID string, items []VectorItem) error {
	if len(items) == 0 {
		return nil
	}
	key := redisKeyFor(agentID)
	pipe := s.rdb.Pipeline()
	for _, it := range items {
		if it.ID == "" || len(it.Embedding) == 0 {
			continue
		}
		rec := redisVectorRecord{
			Vec:  it.Embedding,
			Meta: it.Meta,
		}
		if !it.CreatedAt.IsZero() {
			rec.CreatedAt = it.CreatedAt.Unix()
		}
		b, err := json.Marshal(rec)
		if err != nil {
			logger.L().Warn("redis vector marshal failed, skip",
				zap.String("id", it.ID), zap.Error(err))
			continue
		}
		pipe.HSet(ctx, key, it.ID, b)
	}
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("redis hset pipe: %w", err)
	}
	return nil
}

func (s *redisVectorStore) Search(ctx context.Context, agentID string, qVec []float32, topK int, minSim float64) ([]VectorHit, error) {
	if len(qVec) == 0 {
		return nil, nil
	}
	if topK <= 0 {
		topK = 5
	}
	key := redisKeyFor(agentID)
	raw, err := s.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("redis hgetall: %w", err)
	}
	type scored struct {
		id    string
		score float64
	}
	results := make([]scored, 0, len(raw))
	for id, blob := range raw {
		var rec redisVectorRecord
		if err := json.Unmarshal([]byte(blob), &rec); err != nil {
			logger.L().Warn("redis vector unmarshal failed, skip",
				zap.String("id", id), zap.Error(err))
			continue
		}
		score := cosineSim(qVec, rec.Vec)
		if score >= minSim {
			results = append(results, scored{id: id, score: score})
		}
	}
	// Partial selection sort: only keep topK entries.
	n := len(results)
	for i := 0; i < topK && i < n; i++ {
		best := i
		for j := i + 1; j < n; j++ {
			if results[j].score > results[best].score {
				best = j
			}
		}
		if best != i {
			results[i], results[best] = results[best], results[i]
		}
	}
	if topK < n {
		n = topK
	}
	hits := make([]VectorHit, 0, n)
	for i := 0; i < n; i++ {
		hits = append(hits, VectorHit{ID: results[i].id, Score: results[i].score})
	}
	return hits, nil
}

func (s *redisVectorStore) DeleteByIDs(ctx context.Context, agentID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	key := redisKeyFor(agentID)
	return s.rdb.HDel(ctx, key, ids...).Err()
}

func (s *redisVectorStore) Close() error {
	if s.rdb == nil {
		return nil
	}
	return s.rdb.Close()
}

// ---- helpers ----

const redisDefaultPingTimeout = 3 // seconds

func redisKeyFor(agentID string) string {
	// Redis key rules: strip whitespace / control chars to avoid malformed keys.
	id := strings.TrimSpace(agentID)
	if id == "" {
		id = "unknown-agent"
	}
	return redisKeyPrefix + id
}

// ---- helpers also used by in-memory store (re-exported for convenience) ----

// ParseFloat32Slice parses a comma-separated list like "1.2,3.4,-0.5" into a float32 slice.
// Used only by callers that serialize vectors across boundaries; not part of the main path.
func ParseFloat32Slice(s string) ([]float32, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]float32, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseFloat(p, 32)
		if err != nil {
			return nil, fmt.Errorf("parse float32: %w", err)
		}
		out = append(out, float32(v))
	}
	return out, nil
}
