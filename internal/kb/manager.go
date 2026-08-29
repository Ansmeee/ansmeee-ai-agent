package kb

import (
	"context"
	"fmt"
	"io"
	"strings"

	"ansmeee-ai-agent/internal/models"
	"ansmeee-ai-agent/pkg/logger"

	"go.uber.org/zap"
)

// KBManager 是知识库系统的统一门面，编排 Store / Retriever / Indexer / Embedder。
// 对外暴露两条链路：
//   - 链路 A（隐式注入）：Inject → 检索 + 预算裁剪 → 拼入 system prompt。
//   - 链路 B（显式工具）：Query → 检索 + 返回带引用的完整结果。
type KBManager struct {
	kb        KBStore
	docs      DocStore
	chunks    ChunkStore
	retriever *HybridRetriever
	indexer   Indexer
	embed     Embedder
	vec       VectorStore
}

// NewKBManager 创建知识库管理器。
func NewKBManager(kb KBStore, docs DocStore, chunks ChunkStore, retriever *HybridRetriever, indexer Indexer, embed Embedder, opts ...ManagerOption) *KBManager {
	m := &KBManager{kb: kb, docs: docs, chunks: chunks, retriever: retriever, indexer: indexer, embed: embed}
	for _, o := range opts {
		o(m)
	}
	return m
}

// ManagerOption 构造器扩展选项。
type ManagerOption func(*KBManager)

// WithVectorStore 显式注入向量 store（方便 DeleteDoc / Reindex 清理向量）。
func WithVectorStore(v VectorStore) ManagerOption {
	return func(m *KBManager) { m.vec = v }
}

// ---------- KB 元数据 ----------

// GetKB 获取 agent 的知识库元数据。
func (m *KBManager) GetKB(ctx context.Context, agentID string) (*models.KnowledgeBase, error) {
	return m.kb.GetByAgent(ctx, agentID)
}

// UpsertKB 创建或更新 agent 的知识库配置。
func (m *KBManager) UpsertKB(ctx context.Context, agentID string, patch *models.KnowledgeBase) (*models.KnowledgeBase, error) {
	return m.kb.UpsertByAgent(ctx, agentID, patch)
}

// ensureKB 确保知识库存在，不存在则用默认配置创建（默认启用）。
func (m *KBManager) ensureKB(ctx context.Context, agentID string) (*models.KnowledgeBase, error) {
	kb, err := m.kb.GetByAgent(ctx, agentID)
	if err == nil {
		return kb, nil
	}
	if !isKBNotFound(err) {
		return nil, err
	}
	// 自动创建默认 KB（Enabled/AlwaysInject/ShowCitations 默认开启）
	return m.kb.UpsertByAgent(ctx, agentID, &models.KnowledgeBase{
		Title:         "默认知识库",
		Enabled:       true,
		AlwaysInject:  true,
		ShowCitations: true,
	})
}

func isKBNotFound(err error) bool {
	return err == ErrKBNotFound
}

// ---------- 文档管理 ----------

// AddDocRequest 创建文档的入参。
type AddDocRequest struct {
	Title       string             `json:"title"`
	SourceType  string             `json:"source_type"`
	SourceURL   string             `json:"source_url"`
	FileName    string             `json:"file_name"`
	Content     string             `json:"content"` // text/markdown 原文；url 类型留空（由调用方抓取后填入）
	ParseConfig models.KBParseConfig `json:"parse_config"`
}

// AddDoc 创建文档并同步索引（Phase1）。
func (m *KBManager) AddDoc(ctx context.Context, agentID string, req *AddDocRequest) (*models.KBDoc, error) {
	if req.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if req.SourceType == "" {
		req.SourceType = models.SourceTypeText
	}
	if req.Content == "" && req.SourceType != models.SourceTypeURL {
		return nil, fmt.Errorf("content is required for source_type %q", req.SourceType)
	}
	kb, err := m.ensureKB(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("ensure kb: %w", err)
	}
	applyParseDefaults(&req.ParseConfig)

	doc := &models.KBDoc{
		KBID:        kb.ID,
		AgentID:     agentID,
		Title:       req.Title,
		SourceType:  req.SourceType,
		SourceURL:   req.SourceURL,
		FileName:    req.FileName,
		ParseConfig: req.ParseConfig,
		Status:      models.DocStatusPending,
	}
	docID, err := m.docs.Create(ctx, doc)
	if err != nil {
		return nil, fmt.Errorf("create doc: %w", err)
	}
	// 同步索引
	if err := m.indexer.Index(ctx, docID, strings.NewReader(req.Content)); err != nil {
		// 索引失败已在 indexer 内标记 failed，这里返回 doc 让调用方看到状态
		logger.L().Warn("kb add doc index failed", zap.Int64("doc_id", docID), zap.Error(err))
	}
	return m.docs.Get(ctx, docID)
}

// ListDocs 分页列出知识库文档。
func (m *KBManager) ListDocs(ctx context.Context, agentID string, page, pageSize int) ([]*models.KBDoc, int64, error) {
	kb, err := m.kb.GetByAgent(ctx, agentID)
	if err != nil {
		return nil, 0, err
	}
	return m.docs.ListByKB(ctx, kb.ID, page, pageSize)
}

// GetDoc 获取单个文档。
func (m *KBManager) GetDoc(ctx context.Context, docID int64) (*models.KBDoc, error) {
	return m.docs.Get(ctx, docID)
}

// DeleteDoc 软删除文档，并清理分片 + 更新计数。
// 向量侧的清理留待 Phase2（in-memory 后端重启即失效，且检索时会回查 chunk 文本，
// 已删除的 chunk 不会命中，故不影响正确性）。
// DeleteDoc 删除文档（软删文档 + 删除分片 + 删除向量 + 回滚计数）。
func (m *KBManager) DeleteDoc(ctx context.Context, agentID string, docID int64) error {
	doc, err := m.docs.Get(ctx, docID)
	if err != nil {
		return err
	}
	if doc.AgentID != agentID {
		return ErrDocNotFound
	}
	// 统计待删除分片数（用于回滚计数）
	chunkCount, _ := m.chunks.CountByDoc(ctx, docID)
	// 删除向量（先 ListByDoc 取 vector_id，再删除；ListByDoc 需在 chunks.DeleteByDoc 之前）
	if m.vec != nil {
		if old, lerr := m.chunks.ListByDoc(ctx, docID); lerr != nil {
			logger.L().Warn("kb delete list chunks failed", zap.Int64("doc_id", docID), zap.Error(lerr))
		} else {
			ids := make([]string, 0, len(old))
			for _, c := range old {
				if c.VectorID != "" {
					ids = append(ids, c.VectorID)
				}
			}
			if len(ids) > 0 {
				if derr := m.vec.DeleteByIDs(ctx, agentID, ids); derr != nil {
					logger.L().Warn("kb delete vectors failed", zap.Int64("doc_id", docID), zap.Error(derr))
				}
			}
		}
	}
	// 删除分片
	if derr := m.chunks.DeleteByDoc(ctx, docID); derr != nil {
		logger.L().Warn("kb delete chunks failed", zap.Int64("doc_id", docID), zap.Error(derr))
	}
	// 软删除文档
	if derr := m.docs.Delete(ctx, docID); derr != nil {
		return fmt.Errorf("delete doc: %w", derr)
	}
	// 更新 KB 计数（doc -1, chunk -chunkCount）
	if cerr := m.kb.UpdateCounters(ctx, doc.KBID, -1, -int(chunkCount)); cerr != nil {
		logger.L().Warn("kb update counters (delete) failed", zap.Int64("kb_id", doc.KBID), zap.Error(cerr))
	}
	return nil
}

// ReindexDoc 重新索引文档（需提供新内容）。
func (m *KBManager) ReindexDoc(ctx context.Context, docID int64, content string) error {
	if _, err := m.docs.Get(ctx, docID); err != nil {
		return err
	}
	return m.indexer.Index(ctx, docID, strings.NewReader(content))
}

// ---------- 检索：链路 A（隐式注入）----------

// Inject 执行链路 A 检索并格式化为 system prompt 增强段落。
// 受 KB 配置约束：enabled / always_inject / topK / min_similarity / max_chars_per_turn。
// 任何错误都降级返回空串，绝不阻塞主对话流程。
func (m *KBManager) Inject(ctx context.Context, agentID, query string) string {
	if m == nil || m.retriever == nil {
		return ""
	}
	if query == "" {
		return ""
	}
	kb, err := m.kb.GetByAgent(ctx, agentID)
	if err != nil {
		return ""
	}
	if !kb.Enabled || !kb.AlwaysInject {
		return ""
	}
	chunks, err := m.retriever.RetrieveWithTimeout(ctx, agentID, m.embed, query, kb.TopK, kb.MinSimilarity)
	if err != nil || len(chunks) == 0 {
		return ""
	}
	return formatInjection(chunks, kb.MaxCharsPerTurn, kb.ShowCitations)
}

// formatInjection 将检索结果格式化为增强段落，按预算裁剪。
func formatInjection(chunks []RetrievedChunk, maxChars int, showCitations bool) string {
	var buf strings.Builder
	buf.WriteString("\n【知识库参考】\n")
	used := 0
	for i, c := range chunks {
		// 预算控制：超出则停止
		if maxChars > 0 && used+len(c.Text) > maxChars {
			remaining := maxChars - used
			if remaining <= 0 {
				break
			}
			c.Text = truncateRunes(c.Text, remaining)
		}
		buf.WriteString(fmt.Sprintf("[%d] ", i+1))
		if showCitations && c.DocTitle != "" {
			buf.WriteString(fmt.Sprintf("(来源: %s) ", c.DocTitle))
		}
		buf.WriteString(c.Text)
		buf.WriteString("\n")
		used += len([]rune(c.Text))
		if maxChars > 0 && used >= maxChars {
			break
		}
	}
	return buf.String()
}

// ---------- 检索：链路 B（显式工具）----------

// Query 执行链路 B 显式检索，返回带引用的完整结果。
func (m *KBManager) Query(ctx context.Context, agentID, query string) (*RetrieveResult, error) {
	if m == nil || m.retriever == nil {
		return &RetrieveResult{Chunks: []RetrievedChunk{}}, nil
	}
	kb, err := m.kb.GetByAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if !kb.Enabled {
		return nil, ErrKBDisabled
	}
	chunks, err := m.retriever.RetrieveWithTimeout(ctx, agentID, m.embed, query, kb.TopK, kb.MinSimilarity)
	if err != nil {
		return nil, err
	}
	if chunks == nil {
		chunks = []RetrievedChunk{}
	}
	return &RetrieveResult{
		Chunks:  chunks,
		AgentID: agentID,
		KBID:    kb.ID,
	}, nil
}

// ---------- 辅助 ----------

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// applyParseDefaults 填充切分配置默认值。
func applyParseDefaults(cfg *models.KBParseConfig) {
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = 512
	}
	if cfg.ChunkOverlap < 0 || cfg.ChunkOverlap >= cfg.ChunkSize {
		cfg.ChunkOverlap = cfg.ChunkSize / 4
	}
}

// IndexRaw 暴露给外部直接调用索引（测试 / 内部用）。
func (m *KBManager) IndexRaw(ctx context.Context, docID int64, raw io.Reader) error {
	return m.indexer.Index(ctx, docID, raw)
}
