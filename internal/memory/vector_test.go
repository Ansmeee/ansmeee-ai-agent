package memory

import (
	"context"
	"testing"
	"time"
)

func TestHashEmbedder_DeterministicAndDim(t *testing.T) {
	e := NewHashEmbedder(128)
	a, _ := e.Embed(context.Background(), "我喜欢喝咖啡")
	b, _ := e.Embed(context.Background(), "我喜欢喝咖啡")
	if len(a) != 128 {
		t.Fatalf("dim: got %d want 128", len(a))
	}
	if cosine(a, b) < 0.999 {
		t.Fatalf("same text must embed identically, cosine=%v", cosine(a, b))
	}
	c, _ := e.Embed(context.Background(), "今天天气不错")
	if cosine(a, c) > 0.9 {
		t.Fatalf("unrelated text should be less similar, cosine=%v", cosine(a, c))
	}
}

func TestMemVectorStore_SearchOrdersByCosine(t *testing.T) {
	e := NewHashEmbedder(256)
	st := newMemVectorStore()
	ctx := context.Background()

	texts := []string{"我喜欢喝咖啡", "我住在北京", "我在做一个AI项目"}
	for i, txt := range texts {
		emb, _ := e.Embed(ctx, txt)
		if err := st.Upsert(ctx, VectorItem{UserID: 1, MemoryID: int64(i + 1), Text: txt, Embedding: emb, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}

	q, _ := e.Embed(ctx, "咖啡")
	got, err := st.Search(ctx, 1, q, 3, 0.0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("expected results")
	}
	if got[0].Item.Text != "我喜欢喝咖啡" {
		t.Fatalf("top hit should be the coffee memory, got %q", got[0].Item.Text)
	}
}

func TestMemVectorStore_UpsertDedupByMemoryID(t *testing.T) {
	e := NewHashEmbedder(64)
	st := newMemVectorStore()
	ctx := context.Background()
	emb, _ := e.Embed(ctx, "v1")

	_ = st.Upsert(ctx, VectorItem{UserID: 1, MemoryID: 7, Text: "v1", Embedding: emb})
	emb2, _ := e.Embed(ctx, "v2")
	_ = st.Upsert(ctx, VectorItem{UserID: 1, MemoryID: 7, Text: "v2", Embedding: emb2})

	got, _ := st.Search(ctx, 1, emb2, 10, 0.0)
	if len(got) != 1 {
		t.Fatalf("dedup by MemoryID should keep 1 item, got %d", len(got))
	}
	if got[0].Item.Text != "v2" {
		t.Fatalf("upsert should replace in place, got %q", got[0].Item.Text)
	}
}

func TestRerankVectors_DedupAndOrder(t *testing.T) {
	now := time.Now()
	in := []ScoredVector{
		{Item: VectorItem{Text: "a", CreatedAt: now}, Score: 0.6},
		{Item: VectorItem{Text: "b", CreatedAt: now}, Score: 0.9},
		{Item: VectorItem{Text: "a", CreatedAt: now}, Score: 0.7}, // dup of a, higher
	}
	out := rerankVectors(in, 1.0, 0.95, now)
	if len(out) != 2 {
		t.Fatalf("dedup should yield 2 items, got %d", len(out))
	}
	if out[0].Text != "b" {
		t.Fatalf("highest score should sort first, got %q", out[0].Text)
	}
}
