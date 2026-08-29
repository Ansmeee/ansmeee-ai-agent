package kb

import (
	"context"
	"errors"
	"io"
	"time"

	"ansmeee-ai-agent/internal/models"
)

// ---------- 错误 ----------

var (
	ErrKBNotFound  = errors.New("knowledge base not found")
	ErrKBDisabled  = errors.New("knowledge base disabled")
	ErrDocNotFound = errors.New("kb document not found")
)

// ---------- 检索返回类型 ----------

// RetrievedChunk 是检索命中的单个分片（注入 / Tool 返回共用）。
type RetrievedChunk struct {
	ChunkID   int64   `json:"chunk_id"`
	DocID     int64   `json:"doc_id"`
	DocTitle  string  `json:"doc_title"`
	SourceURL string  `json:"source_url,omitempty"`
	ChunkIdx  int     `json:"chunk_idx"`
	Text      string  `json:"text"`
	Score     float64 `json:"score"`
	Channel   string  `json:"channel"` // "vector" | "keyword"
}

// RetrieveResult 是一次检索的完整结果。
type RetrieveResult struct {
	Chunks      []RetrievedChunk `json:"chunks"`
	BudgetUsed  int              `json:"budget_used"`
	BudgetLimit int              `json:"budget_limit"`
	AgentID     string           `json:"agent_id"`
	KBID        int64            `json:"kb_id"`
	LatencyMs   int64            `json:"latency_ms"`
}

// ---------- CRUD Store 接口 ----------

// KBStore 知识库元数据 CRUD。
type KBStore interface {
	GetByAgent(ctx context.Context, agentID string) (*models.KnowledgeBase, error)
	UpsertByAgent(ctx context.Context, agentID string, patch *models.KnowledgeBase) (*models.KnowledgeBase, error)
	UpdateCounters(ctx context.Context, kbID int64, deltaDocs, deltaChunks int) error
}

// DocStore 文档 CRUD。
type DocStore interface {
	Create(ctx context.Context, doc *models.KBDoc) (int64, error)
	Get(ctx context.Context, docID int64) (*models.KBDoc, error)
	ListByKB(ctx context.Context, kbID int64, page, pageSize int) ([]*models.KBDoc, int64, error)
	UpdateStatus(ctx context.Context, docID int64, status int8, msg string) error
	UpdateMeta(ctx context.Context, docID int64, charCount, chunkCount int) error
	Delete(ctx context.Context, docID int64) error
}

// ChunkStore 分片 CRUD + 关键词检索。
// ChunkStore 分片存储（MySQL / 其他持久化后端），负责 text 全文与 chunk 元信息。
// 向量由独立的 VectorStore 管理；关联靠 KBChunk.VectorID 字段。
type ChunkStore interface {
	BatchUpsert(ctx context.Context, chunks []*models.KBChunk) error
	KeywordSearch(ctx context.Context, agentID, query string, topK int) ([]RetrievedChunk, error)
	DeleteByDoc(ctx context.Context, docID int64) error
	CountByDoc(ctx context.Context, docID int64) (int64, error)
	// ListByDoc 返回文档的所有分片（用于 reindex 时清理旧向量、DeleteDoc 时清理向量）。
	ListByDoc(ctx context.Context, docID int64) ([]*models.KBChunk, error)
	// GetByIDs 批量按 chunk id 查询（用于向量命中后补全文本/元信息）。
	GetByIDs(ctx context.Context, ids []int64) (map[int64]*models.KBChunk, error)
}

// ---------- 解析 / 切分 ----------

// DocParser 从 Reader 解析纯文本。
type DocParser interface {
	Parse(ctx context.Context, r io.Reader, sourceType string) (plain string, err error)
}

// Chunker 递归切分文本为带重叠的分片。
type Chunker interface {
	Chunk(plain string, cfg models.KBParseConfig) []string
}

// ---------- 向量通道 ----------

// Embedder 复用 memory.Embedder 同签名（由 main.go 注入 memory 实现）。
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// VectorItem 是写入向量库的单条记录。
type VectorItem struct {
	ID        string
	Text      string
	Embedding []float32
	Meta      map[string]any
	CreatedAt time.Time
}

// VectorHit 是向量召回的单条命中。
type VectorHit struct {
	ID    string
	Score float64
}

// VectorStore KB 专用向量库（agent_id 命名空间隔离）。
type VectorStore interface {
	Upsert(ctx context.Context, agentID string, items []VectorItem) error
	Search(ctx context.Context, agentID string, qVec []float32, topK int, minSim float64) ([]VectorHit, error)
	DeleteByIDs(ctx context.Context, agentID string, ids []string) error
	Close() error
}

// ---------- 异步索引 ----------

// Indexer 文档索引（Phase1 同步实现，Phase2 扩异步 worker）。
type Indexer interface {
	// Index 同步索引一个文档：解析 → 切分 → 嵌入 → 写库。
	// raw 提供原始内容（text/markdown/html），由调用方负责获取。
	Index(ctx context.Context, docID int64, raw io.Reader) error
}
