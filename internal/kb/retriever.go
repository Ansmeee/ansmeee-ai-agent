package kb

import (
	"context"
	"sort"
	"strconv"
	"time"

	"ansmeee-ai-agent/pkg/logger"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// HybridRetriever 混合检索器：向量通道 + 关键词通道 → RRF 融合重排。
type HybridRetriever struct {
	vec            VectorStore
	chunks         ChunkStore
	vectorWeight   float64
	keywordWeight  float64
}

// NewHybridRetriever 创建混合检索器。
func NewHybridRetriever(vec VectorStore, chunks ChunkStore, vectorWeight, keywordWeight float64) *HybridRetriever {
	if vectorWeight <= 0 && keywordWeight <= 0 {
		vectorWeight, keywordWeight = 0.5, 0.5
	}
	return &HybridRetriever{
		vec:           vec,
		chunks:        chunks,
		vectorWeight:  vectorWeight,
		keywordWeight: keywordWeight,
	}
}

// Retrieve 执行混合检索：并行向量 + 关键词，RRF 融合，返回 TopK。
func (r *HybridRetriever) Retrieve(
	ctx context.Context,
	agentID string,
	queryVec []float32,
	queryText string,
	topK int,
	minSim float64,
) ([]RetrievedChunk, error) {
	if topK <= 0 {
		topK = 5
	}

	var (
		vecHits []RetrievedChunk
		kwHits  []RetrievedChunk
	)

	g, gCtx := errgroup.WithContext(ctx)

	// 向量通道
	if r.vec != nil && len(queryVec) > 0 {
		g.Go(func() error {
			hits, err := r.vec.Search(gCtx, agentID, queryVec, topK, minSim)
			if err != nil {
				logger.L().Warn("kb vector search failed", zap.Error(err))
				return nil // 降级，不中断
			}
			for _, h := range hits {
				chunkID, _ := strconv.ParseInt(h.ID, 10, 64)
				vecHits = append(vecHits, RetrievedChunk{
					ChunkID: chunkID,
					Score:   h.Score,
					Channel: "vector",
				})
			}
			return nil
		})
	}

	// 关键词通道
	g.Go(func() error {
		hits, err := r.chunks.KeywordSearch(gCtx, agentID, queryText, topK)
		if err != nil {
			logger.L().Warn("kb keyword search failed", zap.Error(err))
			return nil
		}
		kwHits = hits
		return nil
	})
	_ = g.Wait()

	// 向量召回的 chunk 需回查文本
	if len(vecHits) > 0 {
		r.fillChunkText(ctx, vecHits)
	}

	// RRF 融合重排
	merged := rrfFuse(vecHits, kwHits, topK, r.vectorWeight, r.keywordWeight)
	return merged, nil
}

// fillChunkText 回查向量命中的 chunk 文本（KeywordSearch 已含文本，向量需补查）。
func (r *HybridRetriever) fillChunkText(ctx context.Context, hits []RetrievedChunk) {
	ids := make([]int64, 0, len(hits))
	for _, h := range hits {
		if h.Text == "" && h.ChunkID != 0 {
			ids = append(ids, h.ChunkID)
		}
	}
	if len(ids) == 0 {
		return
	}
	m, err := r.chunks.GetByIDs(ctx, ids)
	if err != nil {
		logger.L().Warn("kb fillChunkText GetByIDs failed", zap.Error(err))
		return
	}
	for i := range hits {
		if hits[i].Text != "" {
			continue
		}
		if c, ok := m[hits[i].ChunkID]; ok {
			hits[i].Text = c.Text
			hits[i].DocID = c.DocID
			hits[i].DocTitle = c.DocTitle
			hits[i].ChunkIdx = c.ChunkIndex
		}
	}
}

// rrfFuse Reciprocal Rank Fusion 融合两个通道的排名。
// 公式：score = Σ w_i / (k + rank_i)，k=60（业界标准）。
func rrfFuse(vecHits, kwHits []RetrievedChunk, topK int, vecW, kwW float64) []RetrievedChunk {
	const k = 60.0

	// 按 score 降序排（向量）→ 生成 rank
	sort.Slice(vecHits, func(i, j int) bool { return vecHits[i].Score > vecHits[j].Score })
	sort.Slice(kwHits, func(i, j int) bool { return kwHits[i].Score > kwHits[j].Score })

	type fused struct {
		chunk RetrievedChunk
		score float64
	}
	merged := map[int64]*fused{}

	add := func(hits []RetrievedChunk, weight float64) {
		for rank, h := range hits {
			if _, ok := merged[h.ChunkID]; !ok {
				merged[h.ChunkID] = &fused{chunk: h, score: 0}
			}
			merged[h.ChunkID].score += weight / (k + float64(rank+1))
			// 保留有文本的版本
			if h.Text != "" && merged[h.ChunkID].chunk.Text == "" {
				merged[h.ChunkID].chunk.Text = h.Text
				merged[h.ChunkID].chunk.DocID = h.DocID
				merged[h.ChunkID].chunk.DocTitle = h.DocTitle
				merged[h.ChunkID].chunk.ChunkIdx = h.ChunkIdx
			}
		}
	}
	add(vecHits, vecW)
	add(kwHits, kwW)

	result := make([]RetrievedChunk, 0, len(merged))
	for _, f := range merged {
		f.chunk.Score = f.score
		result = append(result, f.chunk)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Score > result[j].Score })
	if len(result) > topK {
		result = result[:topK]
	}
	return result
}

// retrieveTimeout 是链路 A 的默认召回超时。
const retrieveTimeout = 500 * time.Millisecond

// RetrieveWithTimeout 带超时的检索（链路 A 用，不阻塞主流程）。
func (r *HybridRetriever) RetrieveWithTimeout(
	ctx context.Context,
	agentID string,
	embed Embedder,
	queryText string,
	topK int,
	minSim float64,
) ([]RetrievedChunk, error) {
	rctx, cancel := context.WithTimeout(ctx, retrieveTimeout)
	defer cancel()

	var qVec []float32
	if embed != nil {
		v, err := embed.Embed(rctx, queryText)
		if err != nil {
			logger.L().Warn("kb query embed failed", zap.Error(err))
		} else {
			qVec = v
		}
	}
	return r.Retrieve(rctx, agentID, qVec, queryText, topK, minSim)
}
