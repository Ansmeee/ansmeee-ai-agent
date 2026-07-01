package models

import "time"

// Message role constants matching langchaingo's ChatMessageType values.
const (
	RoleSystem   = "system"
	RoleHuman    = "human"
	RoleAI       = "ai"
	RoleGeneric  = "generic"
	RoleFunction = "function"
	RoleTool     = "tool"
)

// ChatMessage represents the ai_chat_session_history table.
type ChatMessage struct {
	ID          int64     `json:"-" gorm:"primaryKey;autoIncrement;column:id"`
	SessionUUID string    `json:"-" gorm:"column:session_uuid;type:char(36);index;not null;default:''"`
	UserID      int64     `json:"user_id" gorm:"column:user_id;not null;default:0;index"`
	UUID        string    `json:"id" gorm:"column:uuid;type:char(36);uniqueIndex;not null;default:''"`
	Content     string    `json:"content" gorm:"column:content;type:text"`
	Role        string    `json:"role" gorm:"column:role;type:varchar(20);not null;default:'human'"`
	CreatedAt   time.Time `json:"created_at" gorm:"column:ctime;autoCreateTime"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"column:mtime;autoUpdateTime"`
}

// TableName overrides the default table name.
func (ChatMessage) TableName() string { return "ai_chat_session_history" }
