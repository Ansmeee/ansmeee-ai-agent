package memory

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"
)

// semanticTimeout bounds one embed+search on the read path (mirrors factTimeout).
const semanticTimeout = 300 * time.Millisecond

// rankedItem is a channel-agnostic recalled memory used for hybrid re-ranking.
type rankedItem struct {
	Text  string
	Score float64
}

// retrieveSemantic embeds the query, searches the vector channel, and renders the
// "## 相关记忆" section. Bounded and degrades to "" on timeout/miss/misconfig.
func (m *MemoryManager) retrieveSemantic(ctx context.Context, userID int64, userMsg string) string {
	if m.vec == nil || m.embed == nil || !m.cfg.Vector.Enabled {
		return ""
	}
	if m.router != nil && m.cfg.Router {
		if class := m.router.Route(userMsg).Class; class == ClassDefault {
			return ""
		}
	}

	vecs := m.recallSemantic(ctx, userID, userMsg)
	if len(vecs) == 0 {
		return ""
	}

	ranked := rerankVectors(vecs, m.cfg.Scoring.Semantic, m.decayFactor(), time.Now())
	lines := make([]string, 0, len(ranked))
	for _, r := range ranked {
		lines = append(lines, "- "+r.Text)
	}
	lines = truncateToBudget(lines, int(m.cfg.BudgetRatio*budgetBaseChars))
	if len(lines) == 0 {
		return ""
	}
	return "## 相关记忆\n" + strings.Join(lines, "\n")
}

// recallSemantic runs embed+search under a bounded context, never blocking the
// read path on a slow embedder or store.
func (m *MemoryManager) recallSemantic(ctx context.Context, userID int64, userMsg string) []ScoredVector {
	cctx, cancel := context.WithTimeout(ctx, semanticTimeout)
	defer cancel()

	type result struct {
		vecs []ScoredVector
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		emb, err := m.embed.Embed(cctx, userMsg)
		if err != nil {
			ch <- result{err: err}
			return
		}
		v, err := m.vec.Search(cctx, userID, emb, m.cfg.Vector.TopK, m.cfg.Vector.MinSimilarity)
		ch <- result{vecs: v, err: err}
	}()

	select {
	case <-cctx.Done():
		m.llog.Warn("semantic recall timed out", zap.Error(cctx.Err()))
		return nil
	case r := <-ch:
		if r.err != nil {
			m.llog.Warn("semantic recall failed", zap.Error(r.err))
			return nil
		}
		return r.vecs
	}
}

func (m *MemoryManager) decayFactor() float64 {
	// Recall scoring uses the same decay as the fact channel; 0.95 is the default.
	return 0.95
}

// rerankVectors orders vector hits by a weighted blend of cosine similarity and
// recency, dedups identical texts (keeping the highest score), and returns them.
func rerankVectors(vecs []ScoredVector, semWeight, decay float64, now time.Time) []rankedItem {
	if semWeight <= 0 {
		semWeight = 1
	}
	seen := make(map[string]int, len(vecs))
	out := make([]rankedItem, 0, len(vecs))
	for _, v := range vecs {
		fresh := freshnessScore(v.Item.CreatedAt, decay, now)
		score := semWeight*v.Score + (1-semWeight)*fresh
		if score < 0 {
			score = v.Score
		}
		if idx, ok := seen[v.Item.Text]; ok {
			if score > out[idx].Score {
				out[idx].Score = score
			}
			continue
		}
		seen[v.Item.Text] = len(out)
		out = append(out, rankedItem{Text: v.Item.Text, Score: score})
	}
	// insertion sort by score desc (small K)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Score > out[j-1].Score; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// freshnessScore is decay^days_since(ref); ref zero → 1 (no penalty).
func freshnessScore(ref time.Time, decay float64, now time.Time) float64 {
	if ref.IsZero() {
		return 1
	}
	if decay <= 0 || decay > 1 {
		decay = 0.95
	}
	days := now.Sub(ref).Hours() / 24
	if days < 0 {
		days = 0
	}
	f := 1.0
	for i := 0; i < int(days); i++ {
		f *= decay
	}
	return f
}
