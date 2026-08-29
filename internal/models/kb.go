package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// ---------- 枚举常量 ----------

const (
	KBStatusActive   int8 = 1
	KBStatusArchived int8 = 2

	DocStatusPending  int8 = 0 // 已上传未索引
	DocStatusIndexing int8 = 1
	DocStatusReady    int8 = 2 // 可检索
	DocStatusFailed   int8 = 3
	DocStatusArchived int8 = 4

	SourceTypeUpload   = "upload"
	SourceTypeURL      = "url"
	SourceTypeText     = "text"
	SourceTypeMarkdown = "markdown"
)

// KnowledgeBase 知识库元数据（MVP: 1 Agent = 1 KB）。
type KnowledgeBase struct {
	ID              int64     `json:"id" gorm:"primaryKey;autoIncrement;column:id"`
	AgentID         string    `json:"agent_id" gorm:"column:agent_id;type:char(36);uniqueIndex;not null"`
	Title           string    `json:"title" gorm:"column:title;type:varchar(255);not null;default:''"`
	Description     string    `json:"description" gorm:"column:description;type:varchar(1000);not null;default:''"`
	Enabled         bool      `json:"enabled" gorm:"column:enabled;type:tinyint(1);not null;default:1"`
	AlwaysInject    bool      `json:"always_inject" gorm:"column:always_inject;type:tinyint(1);not null;default:1"`
	ShowCitations   bool      `json:"show_citations" gorm:"column:show_citations;type:tinyint(1);not null;default:1"`
	TopK            int       `json:"top_k" gorm:"column:top_k;type:int;not null;default:5"`
	MinSimilarity   float64   `json:"min_similarity" gorm:"column:min_similarity;type:double;not null;default:0.6"`
	BudgetRatio     float64   `json:"budget_ratio" gorm:"column:budget_ratio;type:double;not null;default:0.3"`
	MaxCharsPerTurn int       `json:"max_chars_per_turn" gorm:"column:max_chars_per_turn;type:int;not null;default:4000"`
	DocCount        int       `json:"doc_count" gorm:"column:doc_count;type:int;not null;default:0"`
	ChunkCount      int       `json:"chunk_count" gorm:"column:chunk_count;type:int;not null;default:0"`
	Status          int8      `json:"status" gorm:"column:status;type:tinyint;not null;default:1"`
	UpdatedAt       time.Time `json:"updated_at" gorm:"column:mtime;autoUpdateTime"`
	CreatedAt       time.Time `json:"created_at" gorm:"column:ctime;autoCreateTime"`
}

func (KnowledgeBase) TableName() string { return "kb" }

// KBDoc 知识库文档。
type KBDoc struct {
	ID            int64          `json:"id" gorm:"primaryKey;autoIncrement;column:id"`
	KBID          int64          `json:"kb_id" gorm:"column:kb_id;not null;index:idx_kb_agent"`
	AgentID       string         `json:"agent_id" gorm:"column:agent_id;type:char(36);not null;index:idx_kb_agent"`
	Title         string         `json:"title" gorm:"column:title;type:varchar(500);not null;default:''"`
	SourceType    string         `json:"source_type" gorm:"column:source_type;type:varchar(16);not null;default:'upload'"`
	SourceURL     string         `json:"source_url,omitempty" gorm:"column:source_url;type:varchar(1000);not null;default:''"`
	FileName      string         `json:"file_name,omitempty" gorm:"column:file_name;type:varchar(255);not null;default:''"`
	ContentPath   string         `json:"-" gorm:"column:content_path;type:varchar(1000);not null;default:''"`
	CharCount     int            `json:"char_count" gorm:"column:char_count;type:int;not null;default:0"`
	ChunkCount    int            `json:"chunk_count" gorm:"column:chunk_count;type:int;not null;default:0"`
	ParseConfig   KBParseConfig  `json:"parse_config" gorm:"column:parse_config;type:json"`
	Tags          JSONStringSlice `json:"tags,omitempty" gorm:"column:tags;type:json"`
	Status        int8           `json:"status" gorm:"column:status;type:tinyint;not null;default:0"`
	ErrorMsg      string         `json:"error_msg,omitempty" gorm:"column:error_msg;type:varchar(1000);not null;default:''"`
	LastIndexedAt *time.Time     `json:"last_indexed_at,omitempty" gorm:"column:last_indexed_at"`
	UpdatedAt     time.Time      `json:"updated_at" gorm:"column:mtime;autoUpdateTime"`
	CreatedAt     time.Time      `json:"created_at" gorm:"column:ctime;autoCreateTime"`
}

func (KBDoc) TableName() string { return "kb_docs" }

// KBParseConfig 切分配置快照（切分后不可变，保证 chunk 可重现）。
type KBParseConfig struct {
	ChunkSize    int    `json:"chunk_size"`
	ChunkOverlap int    `json:"chunk_overlap"`
	Separator    string `json:"separator"`
}

func (c KBParseConfig) Value() (driver.Value, error) { return json.Marshal(c) }

func (c *KBParseConfig) Scan(v any) error {
	if v == nil {
		return nil
	}
	b, ok := v.([]byte)
	if !ok {
		return fmt.Errorf("KBParseConfig.Scan: expected []byte, got %T", v)
	}
	return json.Unmarshal(b, c)
}

// KBChunk 文档分片（MySQL 存纯文本，供检索后直接上下文 + 关键词 BM25）。
type KBChunk struct {
	ID         int64     `json:"-" gorm:"primaryKey;autoIncrement;column:id"`
	KBID       int64     `json:"kb_id" gorm:"column:kb_id;not null;index:idx_chunk_kb"`
	AgentID    string    `json:"-" gorm:"column:agent_id;type:char(36);not null;index:idx_chunk_agent"`
	DocID      int64     `json:"doc_id" gorm:"column:doc_id;not null;index:idx_chunk_doc"`
	ChunkIndex int       `json:"chunk_index" gorm:"column:chunk_index;type:int;not null;default:0"`
	Text       string    `json:"text" gorm:"column:text;type:text;not null"`
	CharCount  int       `json:"char_count" gorm:"column:char_count;type:int;not null;default:0"`
	DocTitle   string    `json:"doc_title" gorm:"column:doc_title;type:varchar(500);not null;default:''"`
	VectorID   string    `json:"-" gorm:"column:vector_id;type:varchar(64);not null;default:'';index"`
	UpdatedAt  time.Time `json:"-" gorm:"column:mtime;autoUpdateTime"`
	CreatedAt  time.Time `json:"-" gorm:"column:ctime;autoCreateTime"`
}

func (KBChunk) TableName() string { return "kb_chunks" }

// KBIndexJob 异步索引任务（失败可重跑）。
type KBIndexJob struct {
	ID          int64           `json:"id" gorm:"primaryKey;autoIncrement;column:id"`
	DocID       int64           `json:"doc_id" gorm:"column:doc_id;not null;index"`
	AgentID     string          `json:"-" gorm:"column:agent_id;type:char(36);not null;index"`
	Step        string          `json:"step" gorm:"column:step;type:varchar(16);not null;default:'pending'"`
	Progress    int             `json:"progress" gorm:"column:progress;type:int;not null;default:0"`
	ProgressMsg string          `json:"progress_msg,omitempty" gorm:"column:progress_msg;type:varchar(500);not null;default:''"`
	Stats       KBIndexJobStats `json:"stats,omitempty" gorm:"column:stats;type:json"`
	ErrorMsg    string          `json:"error_msg,omitempty" gorm:"column:error_msg;type:varchar(2000);not null;default:''"`
	StartedAt   *time.Time      `json:"started_at,omitempty" gorm:"column:started_at"`
	FinishedAt  *time.Time      `json:"finished_at,omitempty" gorm:"column:finished_at"`
	CreatedAt   time.Time       `json:"-" gorm:"column:ctime;autoCreateTime"`
}

func (KBIndexJob) TableName() string { return "kb_index_jobs" }

// KBIndexJobStats 索引耗时统计。
type KBIndexJobStats struct {
	ParseMs    int `json:"parse_ms"`
	ChunkMs    int `json:"chunk_ms"`
	EmbedMs    int `json:"embed_ms"`
	WriteMs    int `json:"write_ms"`
	Chunks     int `json:"chunks"`
	CharCount  int `json:"char_count"`
	TokenUsage int `json:"token_usage"`
}

func (s KBIndexJobStats) Value() (driver.Value, error) { return json.Marshal(s) }

func (s *KBIndexJobStats) Scan(v any) error {
	if v == nil {
		return nil
	}
	b, ok := v.([]byte)
	if !ok {
		return fmt.Errorf("KBIndexJobStats.Scan: expected []byte, got %T", v)
	}
	return json.Unmarshal(b, s)
}
