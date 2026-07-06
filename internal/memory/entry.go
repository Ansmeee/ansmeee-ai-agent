package memory

import "time"

// MemoryEntry is an L2 long-term memory record (fact / policy channels share this table).
type MemoryEntry struct {
	ID          int64   `gorm:"primaryKey;autoIncrement"`
	UserID      int64   `gorm:"index:idx_user_channel;index:idx_user_key;not null;uniqueIndex:uk_uakv,priority:1"`
	AgentID     string  `gorm:"type:varchar(36);not null;default:'';uniqueIndex:uk_uakv,priority:2"` // '' not NULL → user-level memory
	Channel     string  `gorm:"type:varchar(16);index:idx_user_channel;not null"`         // fact | policy
	Kind        string  `gorm:"type:varchar(16);not null"`                                // fact / preference / policy / summary
	KeyName     string  `gorm:"type:varchar(128);index:idx_user_key;not null;uniqueIndex:uk_uakv,priority:3"`
	Value       string  `gorm:"type:text;not null"`
	ValueHash   string  `gorm:"type:char(32);not null;uniqueIndex:uk_uakv,priority:4"` // md5(value) for multi-value dedup
	Cardinality string  `gorm:"type:varchar(8);default:'multi'"`            // single | multi
	Confidence  float64 `gorm:"not null;default:1.0"`
	Evidence    string  `gorm:"type:json"`                        // [{session_id, message_id}]
	Status      string  `gorm:"type:varchar(8);default:'active'"` // active | archived
	Source      string  `gorm:"type:varchar(16);default:'rule'"`  // rule | user_stated | llm_extracted
	HitCount    int     `gorm:"default:0"`
	LastUsedAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ExpiresAt   *time.Time
}

// TableName overrides the default table name.
func (MemoryEntry) TableName() string { return "memory_entries" }

// SessionSummary stores a per-session LLM summary (L2 secondary path, Phase 2).
type SessionSummary struct {
	ID        int64  `gorm:"primaryKey;autoIncrement"`
	SessionID string `gorm:"type:varchar(36);index"`
	UserID    int64  `gorm:"index"`
	Summary   string `gorm:"type:text"`
	Topics    string `gorm:"type:json"`
	CreatedAt time.Time
}

// TableName overrides the default table name.
func (SessionSummary) TableName() string { return "session_summaries" }
