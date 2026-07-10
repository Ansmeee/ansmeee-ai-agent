package memory

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"time"

	"ansmeee-ai-agent/internal/config"

	"github.com/tmc/langchaingo/llms/openai"
)

// Embedder turns text into a dense vector for semantic recall.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// VectorItem is a single semantic memory record.
type VectorItem struct {
	UserID    int64
	MemoryID  int64 // 0 when the item is not backed by a memory_entries row (e.g. summary)
	Kind      string
	Text      string
	Embedding []float32
	CreatedAt time.Time
}

// ScoredVector is a recalled vector item with its cosine similarity.
type ScoredVector struct {
	Item  VectorItem
	Score float64
}

// VectorStore is the pluggable semantic memory channel.
type VectorStore interface {
	Upsert(ctx context.Context, item VectorItem) error
	Search(ctx context.Context, userID int64, emb []float32, topK int, minSim float64) ([]ScoredVector, error)
	Close() error
}

// --- embedders ---

// hashEmbedder is a deterministic, dependency-free embedder used as a degraded
// fallback and in tests. It maps tokens into a fixed-dim bag-of-hashes vector.
type hashEmbedder struct {
	dim int
}

// NewHashEmbedder builds a deterministic embedder of the given dimension.
func NewHashEmbedder(dim int) Embedder {
	if dim <= 0 {
		dim = 256
	}
	return &hashEmbedder{dim: dim}
}

func (h *hashEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, h.dim)
	for tok := range strings.FieldsSeq(strings.ToLower(text)) {
		hs := fnv.New32a()
		_, _ = hs.Write([]byte(tok))
		vec[hs.Sum32()%uint32(h.dim)] += 1
	}
	// also fold in rune bigrams so CJK text (no spaces) still yields signal
	runes := []rune(strings.ToLower(text))
	for i := 0; i+1 < len(runes); i++ {
		hs := fnv.New32a()
		_, _ = hs.Write([]byte(string(runes[i : i+2])))
		vec[hs.Sum32()%uint32(h.dim)] += 1
	}
	normalize(vec)
	return vec, nil
}

// openaiEmbedder calls an OpenAI-compatible embeddings endpoint via langchaingo.
type openaiEmbedder struct {
	client *openai.LLM
}

// NewOpenAIEmbedder builds an embedder from the LLM config and embedding model.
func NewOpenAIEmbedder(cfg *config.LLMConfig, model string) (Embedder, error) {
	if model == "" {
		model = "text-embedding-3-small"
	}
	client, err := openai.New(
		openai.WithToken(cfg.APIKey),
		openai.WithBaseURL(cfg.BaseURL),
		openai.WithEmbeddingModel(model),
	)
	if err != nil {
		return nil, err
	}
	return &openaiEmbedder{client: client}, nil
}

func (e *openaiEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	embs, err := e.client.CreateEmbedding(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embs) == 0 {
		return nil, nil
	}
	v := embs[0]
	normalize(v)
	return v, nil
}

// --- vector math helpers ---

// normalize scales v to unit length in place (no-op for a zero vector).
func normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
}

// cosine returns the cosine similarity of two equal-length vectors.
func cosine(a, b []float32) float64 {
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
