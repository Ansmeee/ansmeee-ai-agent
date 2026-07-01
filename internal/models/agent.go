package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// Agent status constants.
const (
	AgentStatusEnabled  int8 = 1
	AgentStatusDisabled int8 = 2
)

// Agent represents the ai_agent table.
type Agent struct {
	ID            int64             `json:"-" gorm:"primaryKey;autoIncrement;column:id"`
	UUID          string            `json:"id" gorm:"column:uuid;type:char(36);uniqueIndex;not null;default:''"`
	UserID        int64             `json:"user_id" gorm:"column:user_id;not null;default:0;index"`
	Title         string            `json:"title" gorm:"column:title;type:varchar(255);not null;default:''"`
	Description   string            `json:"description" gorm:"column:intro;type:varchar(1000);not null;default:''"`
	Prompt        string            `json:"prompt" gorm:"column:prompt;type:text"`
	Tools         JSONStringSlice `json:"tools" gorm:"column:tools;type:json"`
	Model         string          `json:"model" gorm:"column:model;type:varchar(100);not null;default:''"`
	Temperature   *float64        `json:"temperature" gorm:"column:temperature"`
	MaxTokens     int             `json:"max_tokens" gorm:"column:max_tokens;type:int;not null;default:0"`
	TopP          *float64        `json:"top_p" gorm:"column:top_p"`
	MaxIterations int8            `json:"max_iterations" gorm:"column:max_iterations;type:tinyint;not null;default:5"`
	Status        int8              `json:"status" gorm:"column:status;type:tinyint;not null;default:1"`
	UpdatedAt     time.Time         `json:"updated_at" gorm:"column:mtime;autoUpdateTime"`
	CreatedAt     time.Time         `json:"created_at" gorm:"column:ctime;autoCreateTime"`
}

// TableName overrides the default table name.
func (Agent) TableName() string { return "ai_agent" }

// JSONStringSlice is a []string that serializes to/from JSON in the database.
type JSONStringSlice []string

// Value implements driver.Valuer for GORM JSON serialization.
func (s JSONStringSlice) Value() (driver.Value, error) {
	if s == nil {
		return nil, nil
	}
	return json.Marshal(s)
}

// Scan implements sql.Scanner for GORM JSON deserialization.
func (s *JSONStringSlice) Scan(value any) error {
	if value == nil {
		*s = nil
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("JSONStringSlice.Scan: expected []byte, got %T", value)
	}
	return json.Unmarshal(bytes, s)
}
