package kb

import (
	"context"
	"fmt"
	"io"
	"time"

	"ansmeee-ai-agent/internal/models"
	"ansmeee-ai-agent/pkg/logger"

	"go.uber.org/zap"
)

// syncIndexer Phase1 同步索引器：解析 → 切分 → 嵌入 → 写库。
// 全流程在调用方 goroutine 内完成，失败标记 doc 状态为 failed。
type syncIndexer struct {
	kb      KBStore
	docs    DocStore
	chunks  ChunkStore
	vec     VectorStore
	parser  DocParser
	chunker Chunker
	embed   Embedder
}

// NewIndexer 创建同步索引器。
func NewIndexer(kb KBStore, docs DocStore, chunks ChunkStore, vec VectorStore, parser DocParser, chunker Chunker, embed Embedder) Indexer {
	return &syncIndexer{kb: kb, docs: docs, chunks: chunks, vec: vec, parser: parser, chunker: chunker, embed: embed}
}

// Index 执行完整索引流水线。
func (x *syncIndexer) Index(ctx context.Context, docID int64, raw io.Reader) error {
	doc, err := x.docs.Get(ctx, docID)
	if err != nil {
		return fmt.Errorf("load doc: %w", err)
	}
	if err := x.docs.UpdateStatus(ctx, docID, models.DocStatusIndexing, ""); err != nil {
		return fmt.Errorf("mark indexing: %w", err)
	}

	// 失败兜底：任意步骤出错都把 doc 标记为 failed。
	defer func() {
		if err != nil {
			_ = x.docs.UpdateStatus(ctx, docID, models.DocStatusFailed, truncateErr(err.Error()))
			logger.L().Error("kb index failed", zap.Int64("doc_id", docID), zap.Error(err))
		}
	}()

	// 1. 解析
	t0 := time.Now()
	plain, perr := x.parser.Parse(ctx, raw, doc.SourceType)
	if perr != nil {
		err = fmt.Errorf("parse: %w", perr)
		return err
	}
	if plain == "" {
		err = fmt.Errorf("parse: empty content")
		return err
	}
	parseMs := time.Since(t0).Milliseconds()

	// 2. 切分
	t1 := time.Now()
	pieces := x.chunker.Chunk(plain, doc.ParseConfig)
	if len(pieces) == 0 {
		err = fmt.Errorf("chunk: no pieces produced")
		return err
	}
	chunkMs := time.Since(t1).Milliseconds()

	// 3. 嵌入 + 组装 chunk 记录
	t2 := time.Now()
	chunkModels := make([]*models.KBChunk, 0, len(pieces))
	vecItems := make([]VectorItem, 0, len(pieces))
	for i, text := range pieces {
		cm := &models.KBChunk{
			KBID:       doc.KBID,
			AgentID:    doc.AgentID,
			DocID:      doc.ID,
			ChunkIndex: i,
			Text:       text,
			CharCount:  len([]rune(text)),
			DocTitle:   doc.Title,
		}
		chunkModels = append(chunkModels, cm)
		// 嵌入该分片
		if x.embed != nil {
			vec, eerr := x.embed.Embed(ctx, text)
			if eerr != nil {
				logger.L().Warn("kb chunk embed failed, skip vector", zap.Int64("doc_id", docID), zap.Int("chunk_idx", i), zap.Error(eerr))
			} else if len(vec) > 0 {
				vecItems = append(vecItems, VectorItem{
					Embedding: vec,
					Meta:      map[string]any{"doc_id": doc.ID, "chunk_idx": i},
				})
			}
		}
	}
	embedMs := time.Since(t2).Milliseconds()

	// 4. 写库
	t3 := time.Now()
	// 重新索引场景：先清理旧分片对应的向量记录（需在 DeleteByDoc 之前执行，
	// 因为 ListByDoc 依赖 chunk 表存在的旧记录取 vector_id）。
	if doc.ChunkCount > 0 && x.vec != nil {
		if old, lerr := x.chunks.ListByDoc(ctx, docID); lerr != nil {
			logger.L().Warn("kb list old chunks failed, skip vector purge",
				zap.Int64("doc_id", docID), zap.Error(lerr))
		} else {
			ids := make([]string, 0, len(old))
			for _, c := range old {
				if c.VectorID != "" {
					ids = append(ids, c.VectorID)
				}
			}
			if len(ids) > 0 {
				if derr := x.vec.DeleteByIDs(ctx, doc.AgentID, ids); derr != nil {
					logger.L().Warn("kb purge old vectors failed",
						zap.Int64("doc_id", docID), zap.Error(derr))
				}
			}
		}
	}
	// 清理旧分片（重新索引场景）
	if cerr := x.chunks.DeleteByDoc(ctx, docID); cerr != nil {
		logger.L().Warn("kb delete old chunks failed", zap.Int64("doc_id", docID), zap.Error(cerr))
	}
	if werr := x.chunks.BatchUpsert(ctx, chunkModels); werr != nil {
		err = fmt.Errorf("write chunks: %w", werr)
		return err
	}
	// 向量写入：vector_id = chunk_id 字符串，需在 chunk 落库后回填
	if x.vec != nil && len(vecItems) > 0 {
		for i := range chunkModels {
			if i < len(vecItems) {
				vid := fmt.Sprintf("%d", chunkModels[i].ID)
				chunkModels[i].VectorID = vid
				vecItems[i].ID = vid
			}
		}
		if verr := x.vec.Upsert(ctx, doc.AgentID, vecItems); verr != nil {
			logger.L().Warn("kb vector upsert failed", zap.Int64("doc_id", docID), zap.Error(verr))
		}
		// 回写 vector_id 到 MySQL（非关键路径，失败仅告警）
		x.persistVectorIDs(ctx, chunkModels)
	}
	writeMs := time.Since(t3).Milliseconds()

	// 5. 更新 doc 元数据 + KB 计数
	charCount := len([]rune(plain))
	if merr := x.docs.UpdateMeta(ctx, docID, charCount, len(pieces)); merr != nil {
		logger.L().Warn("kb update doc meta failed", zap.Int64("doc_id", docID), zap.Error(merr))
	}
	// 重索引时按差值更新计数更准确，但 Phase1 简化：首次索引才累加。
	// 用旧 chunk_count 是否为 0 判断是否首次。
	deltaChunks := len(pieces) - int(doc.ChunkCount)
	if doc.ChunkCount == 0 {
		// 首次：doc+1, chunk+delta（=len）
		if cerr := x.kb.UpdateCounters(ctx, doc.KBID, 1, len(pieces)); cerr != nil {
			logger.L().Warn("kb update counters failed", zap.Int64("kb_id", doc.KBID), zap.Error(cerr))
		}
	} else if deltaChunks != 0 {
		if cerr := x.kb.UpdateCounters(ctx, doc.KBID, 0, deltaChunks); cerr != nil {
			logger.L().Warn("kb update counters (delta) failed", zap.Int64("kb_id", doc.KBID), zap.Error(cerr))
		}
	}

	now := time.Now()
	_ = x.docs.UpdateStatus(ctx, docID, models.DocStatusReady, "")
	_ = x.touchIndexedAt(ctx, docID, now)

	logger.L().Info("kb index done",
		zap.Int64("doc_id", docID),
		zap.Int("chunks", len(pieces)),
		zap.Int64("parse_ms", parseMs),
		zap.Int64("chunk_ms", chunkMs),
		zap.Int64("embed_ms", embedMs),
		zap.Int64("write_ms", writeMs),
	)
	return nil
}

// persistVectorIDs 回写 vector_id 到 kb_chunks（批量）。
func (x *syncIndexer) persistVectorIDs(ctx context.Context, chunks []*models.KBChunk) {
	for _, c := range chunks {
		if c.VectorID == "" {
			continue
		}
		// 轻量更新，失败仅告警
		if err := x.updateVectorID(ctx, c.ID, c.VectorID); err != nil {
			logger.L().Warn("kb persist vector_id failed", zap.Int64("chunk_id", c.ID), zap.Error(err))
		}
	}
}

// updateVectorID 单条更新 vector_id（通过 chunks store 的底层 db）。
func (x *syncIndexer) updateVectorID(ctx context.Context, chunkID int64, vectorID string) error {
	if gs, ok := x.chunks.(*gormChunkStore); ok {
		return gs.db.WithContext(ctx).Model(&models.KBChunk{}).
			Where("id = ?", chunkID).Update("vector_id", vectorID).Error
	}
	return nil
}

// touchIndexedAt 更新 last_indexed_at。
func (x *syncIndexer) touchIndexedAt(ctx context.Context, docID int64, t time.Time) error {
	if gs, ok := x.docs.(*gormDocStore); ok {
		return gs.db.WithContext(ctx).Model(&models.KBDoc{}).
			Where("id = ?", docID).Update("last_indexed_at", t).Error
	}
	return nil
}

// truncateErr 截断错误信息避免超长。
func truncateErr(s string) string {
	const max = 900
	r := []rune(s)
	if len(r) > max {
		return string(r[:max])
	}
	return s
}
