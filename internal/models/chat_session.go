package models

import "time"

// Session represents the ai_chat_session table.
type Session struct {
	ID        int64     `json:"-" gorm:"primaryKey;autoIncrement;column:id"`
	UUID      string    `json:"id" gorm:"column:uuid;type:char(36);uniqueIndex;not null;default:''"`
	UserID    int64     `json:"user_id" gorm:"column:user_id;not null;default:0;index"`
	Title     string    `json:"title" gorm:"column:title;type:varchar(255);not null;default:''"`
	AgentUUID string    `json:"agent_id" gorm:"column:agent_uuid;type:char(36);not null;default:''"`
	CreatedAt time.Time `json:"created_at" gorm:"column:ctime;autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"column:mtime;autoUpdateTime"`
	// L2 memory: idle-scan scheduling for the backend-agnostic summary trigger.
	LastActiveAt *time.Time `json:"-" gorm:"column:last_active_at"`
	Summarized   bool       `json:"-" gorm:"column:summarized;not null;default:0"`
}

// TableName overrides the default table name.
func (Session) TableName() string { return "ai_chat_session" }
