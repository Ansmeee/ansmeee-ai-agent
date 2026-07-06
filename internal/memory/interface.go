package memory

import (
	"context"
	"time"
)

// Message represents a single chat message stored in memory.
type Message struct {
	Role    string            `json:"role"`
	Content string            `json:"content"`
	ID      string            `json:"id,omitempty"`   // message-level id, for evidence references
	Ts      time.Time         `json:"ts,omitzero"`    // creation timestamp
	Meta    map[string]string `json:"meta,omitempty"` // extension markers
}

// SessionInfo is a summary of a session.
type SessionInfo struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	AgentID   string `json:"agent_id,omitempty"`
	CreatedAt string `json:"created_at"`
}

// SessionStore defines the interface for session-based conversation memory.
type SessionStore interface {
	// AddMessage appends a message to a session.
	AddMessage(ctx context.Context, sessionID string, msg Message, userID int64) error
	// History returns all messages for a session.
	History(ctx context.Context, sessionID string) ([]Message, error)
	// Exists checks whether a session exists.
	Exists(ctx context.Context, sessionID string) (bool, error)
	// Delete removes a session and all its messages.
	Delete(ctx context.Context, sessionID string) error
	// ListSessions returns active sessions, optionally filtered by agentID.
	ListSessions(ctx context.Context, userID int64, agentID string) ([]SessionInfo, error)
	// SetAgent records which agent is used for a session.
	SetAgent(ctx context.Context, sessionID, agentID string, userID int64) error
	// GetAgent returns the agent for a session.
	GetAgent(ctx context.Context, sessionID string) (string, error)
	// Close releases any resources held by the store.
	Close() error
}
