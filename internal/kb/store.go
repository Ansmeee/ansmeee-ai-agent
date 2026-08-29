package kb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"ansmeee-ai-agent/internal/models"
	"ansmeee-ai-agent/pkg/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ---------- KBStore ----------

// gormKBStore GORM 实现的知识库元数据 Store。
type gormKBStore struct {
	db *gorm.DB
}

// NewKBStore 创建知识库 Store，并 AutoMigrate。
func NewKBStore(db *gorm.DB) KBStore {
	_ = db.AutoMigrate(&models.KnowledgeBase{})
	return &gormKBStore{db: db}
}

func (s *gormKBStore) GetByAgent(ctx context.Context, agentID string) (*models.KnowledgeBase, error) {
	var kb models.KnowledgeBase
	if err := s.db.WithContext(ctx).Where("agent_id = ?", agentID).First(&kb).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrKBNotFound
		}
		return nil, fmt.Errorf("query kb: %w", err)
	}
	return &kb, nil
}

func (s *gormKBStore) UpsertByAgent(ctx context.Context, agentID string, patch *models.KnowledgeBase) (*models.KnowledgeBase, error) {
	_, err := s.GetByAgent(ctx, agentID)
	if err != nil && !errors.Is(err, ErrKBNotFound) {
		return nil, err
	}
	if errors.Is(err, ErrKBNotFound) {
		// 创建
		kb := &models.KnowledgeBase{
			AgentID:         agentID,
			Title:           patch.Title,
			Description:     patch.Description,
			Enabled:         patch.Enabled,
			AlwaysInject:    patch.AlwaysInject,
			ShowCitations:   patch.ShowCitations,
			TopK:            patch.TopK,
			MinSimilarity:   patch.MinSimilarity,
			BudgetRatio:     patch.BudgetRatio,
			MaxCharsPerTurn: patch.MaxCharsPerTurn,
			Status:          models.KBStatusActive,
		}
		applyKBDefaults(kb)
		if err := s.db.WithContext(ctx).Create(kb).Error; err != nil {
			return nil, fmt.Errorf("create kb: %w", err)
		}
		return kb, nil
	}
	// 更新（仅更新允许的字段）
	updates := map[string]any{}
	if patch.Title != "" {
		updates["title"] = patch.Title
	}
	if patch.Description != "" {
		updates["description"] = patch.Description
	}
	updates["enabled"] = patch.Enabled
	updates["always_inject"] = patch.AlwaysInject
	updates["show_citations"] = patch.ShowCitations
	if patch.TopK > 0 {
		updates["top_k"] = patch.TopK
	}
	if patch.MinSimilarity > 0 {
		updates["min_similarity"] = patch.MinSimilarity
	}
	if patch.BudgetRatio > 0 {
		updates["budget_ratio"] = patch.BudgetRatio
	}
	if patch.MaxCharsPerTurn > 0 {
		updates["max_chars_per_turn"] = patch.MaxCharsPerTurn
	}
	if len(updates) > 0 {
		if err := s.db.WithContext(ctx).Model(&models.KnowledgeBase{}).
			Where("agent_id = ?", agentID).Updates(updates).Error; err != nil {
			return nil, fmt.Errorf("update kb: %w", err)
		}
	}
	return s.GetByAgent(ctx, agentID)
}

func (s *gormKBStore) UpdateCounters(ctx context.Context, kbID int64, deltaDocs, deltaChunks int) error {
	return s.db.WithContext(ctx).Model(&models.KnowledgeBase{}).
		Where("id = ?", kbID).
		Updates(map[string]any{
			"doc_count":   gorm.Expr("doc_count + ?", deltaDocs),
			"chunk_count": gorm.Expr("chunk_count + ?", deltaChunks),
		}).Error
}

// ---------- DocStore ----------

// gormDocStore GORM 实现的文档 Store。
type gormDocStore struct {
	db *gorm.DB
}

// NewDocStore 创建文档 Store，并 AutoMigrate。
func NewDocStore(db *gorm.DB) DocStore {
	_ = db.AutoMigrate(&models.KBDoc{}, &models.KBIndexJob{})
	return &gormDocStore{db: db}
}

func (s *gormDocStore) Create(ctx context.Context, doc *models.KBDoc) (int64, error) {
	if err := s.db.WithContext(ctx).Create(doc).Error; err != nil {
		return 0, fmt.Errorf("create kb doc: %w", err)
	}
	return doc.ID, nil
}

func (s *gormDocStore) Get(ctx context.Context, docID int64) (*models.KBDoc, error) {
	var doc models.KBDoc
	if err := s.db.WithContext(ctx).Where("id = ?", docID).First(&doc).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDocNotFound
		}
		return nil, fmt.Errorf("query kb doc: %w", err)
	}
	return &doc, nil
}

func (s *gormDocStore) ListByKB(ctx context.Context, kbID int64, page, pageSize int) ([]*models.KBDoc, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	var total int64
	if err := s.db.WithContext(ctx).Model(&models.KBDoc{}).
		Where("kb_id = ? AND status != ?", kbID, models.DocStatusArchived).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var docs []*models.KBDoc
	offset := (page - 1) * pageSize
	if err := s.db.WithContext(ctx).
		Where("kb_id = ? AND status != ?", kbID, models.DocStatusArchived).
		Order("id DESC").Offset(offset).Limit(pageSize).Find(&docs).Error; err != nil {
		return nil, 0, err
	}
	return docs, total, nil
}

func (s *gormDocStore) UpdateStatus(ctx context.Context, docID int64, status int8, msg string) error {
	return s.db.WithContext(ctx).Model(&models.KBDoc{}).
		Where("id = ?", docID).
		Updates(map[string]any{"status": status, "error_msg": msg}).Error
}

func (s *gormDocStore) UpdateMeta(ctx context.Context, docID int64, charCount, chunkCount int) error {
	return s.db.WithContext(ctx).Model(&models.KBDoc{}).
		Where("id = ?", docID).
		Updates(map[string]any{
			"char_count":  charCount,
			"chunk_count": chunkCount,
			"status":      models.DocStatusReady,
			"error_msg":   "",
		}).Error
}

func (s *gormDocStore) Delete(ctx context.Context, docID int64) error {
	return s.db.WithContext(ctx).Model(&models.KBDoc{}).
		Where("id = ?", docID).
		Update("status", models.DocStatusArchived).Error
}

// ---------- ChunkStore ----------

// gormChunkStore GORM 实现的分片 Store。
type gormChunkStore struct {
	db *gorm.DB
}

// NewChunkStore 创建分片 Store，并 AutoMigrate。
func NewChunkStore(db *gorm.DB) ChunkStore {
	_ = db.AutoMigrate(&models.KBChunk{})
	return &gormChunkStore{db: db}
}

func (s *gormChunkStore) BatchUpsert(ctx context.Context, chunks []*models.KBChunk) error {
	if len(chunks) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).CreateInBatches(chunks, 100).Error
}

// KeywordSearch 使用 MySQL FULLTEXT (ngram) 做关键词检索。
// 若 FULLTEXT 不可用（如 SQLite 测试环境），降级为 LIKE 模糊匹配。
func (s *gormChunkStore) KeywordSearch(ctx context.Context, agentID, query string, topK int) ([]RetrievedChunk, error) {
	if topK <= 0 {
		topK = 5
	}
	query = strings.TrimSpace(query)
	var chunks []models.KBChunk
	// 优先 FULLTEXT（Raw 保证 ORDER BY 也能正确参数化），失败降级 LIKE
	err := s.db.WithContext(ctx).Raw(
		"SELECT * FROM kb_chunks WHERE agent_id = ? AND MATCH(text) AGAINST(? IN NATURAL LANGUAGE MODE) ORDER BY MATCH(text) AGAINST(? IN NATURAL LANGUAGE MODE) DESC LIMIT ?",
		agentID, query, query, topK,
	).Scan(&chunks).Error
	if err != nil || len(chunks) == 0 {
		if err != nil {
			logger.L().Warn("kb fulltext search failed, fallback to LIKE", zap.Error(err))
		}
		like := "%" + query + "%"
		if qerr := s.db.WithContext(ctx).
			Where("agent_id = ? AND text LIKE ?", agentID, like).
			Order("id DESC").Limit(topK).Find(&chunks).Error; qerr != nil {
			return nil, fmt.Errorf("keyword search: %w", qerr)
		}
	}
	results := make([]RetrievedChunk, 0, len(chunks))
	for i := range chunks {
		results = append(results, RetrievedChunk{
			ChunkID:  chunks[i].ID,
			DocID:    chunks[i].DocID,
			DocTitle: chunks[i].DocTitle,
			ChunkIdx: chunks[i].ChunkIndex,
			Text:     chunks[i].Text,
			Score:    0.5, // LIKE 降级无真实 BM25 分数，给固定中值
			Channel:  "keyword",
		})
	}
	return results, nil
}

func (s *gormChunkStore) DeleteByDoc(ctx context.Context, docID int64) error {
	return s.db.WithContext(ctx).Where("doc_id = ?", docID).Delete(&models.KBChunk{}).Error
}

func (s *gormChunkStore) CountByDoc(ctx context.Context, docID int64) (int64, error) {
	var count int64
	if err := s.db.WithContext(ctx).Model(&models.KBChunk{}).Where("doc_id = ?", docID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// ListByDoc 返回文档的所有分片（用于 reindex / delete 时清理旧向量）。
func (s *gormChunkStore) ListByDoc(ctx context.Context, docID int64) ([]*models.KBChunk, error) {
	var chunks []*models.KBChunk
	if err := s.db.WithContext(ctx).Where("doc_id = ?", docID).Find(&chunks).Error; err != nil {
		return nil, err
	}
	return chunks, nil
}

// GetByIDs 批量按 chunk_id 查询（向量命中后补全文本/元信息）。
func (s *gormChunkStore) GetByIDs(ctx context.Context, ids []int64) (map[int64]*models.KBChunk, error) {
	out := make(map[int64]*models.KBChunk, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var chunks []*models.KBChunk
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&chunks).Error; err != nil {
		return nil, err
	}
	for _, c := range chunks {
		out[c.ID] = c
	}
	return out, nil
}

// ---------- 默认值 ----------

func applyKBDefaults(kb *models.KnowledgeBase) {
	if kb.TopK == 0 {
		kb.TopK = 5
	}
	if kb.MinSimilarity == 0 {
		kb.MinSimilarity = 0.6
	}
	if kb.BudgetRatio == 0 {
		kb.BudgetRatio = 0.3
	}
	if kb.MaxCharsPerTurn == 0 {
		kb.MaxCharsPerTurn = 4000
	}
}
