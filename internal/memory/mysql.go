package memory

import (
	"context"
	"fmt"
	"time"

	"ansmeee-ai-agent/internal/models"
	"ansmeee-ai-agent/internal/tracing"
	"ansmeee-ai-agent/pkg/logger"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MySQLStore implements SessionStore backed by MySQL via GORM.
type MySQLStore struct {
	db *gorm.DB
}

// NewMySQLStore creates a new MySQL session store.
func NewMySQLStore(db *gorm.DB) (*MySQLStore, error) {
	return &MySQLStore{db: db}, nil
}

func genMsgUUID() string {
	return uuid.New().String()
}

// AddMessage appends a message to a session.
func (s *MySQLStore) AddMessage(ctx context.Context, sessionID string, msg Message, userID int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&models.ChatMessage{
			SessionUUID: sessionID,
			UserID:      userID,
			UUID:        genMsgUUID(),
			Content:     msg.Content,
			Role:        msg.Role,
		}).Error; err != nil {
			return err
		}
		// Bump idle-scan activity marker on every message; a new human turn
		// reopens the session for summarization (summarized reset to false).
		sessionUpdates := map[string]any{"last_active_at": time.Now()}
		if msg.Role == models.RoleHuman {
			sessionUpdates["summarized"] = false
		}
		if err := tx.Model(&models.Session{}).Where("uuid = ?", sessionID).
			Updates(sessionUpdates).Error; err != nil {
			logger.L().Warn("update session activity failed", tracing.ErrFields(ctx, err)...)
		}
		if msg.Role == models.RoleHuman {
			if err := tx.Model(&models.Session{}).Where("uuid = ? AND title = ''", sessionID).
				Update("title", msg.Content).Error; err != nil {
				logger.L().Warn("backfill session title failed", tracing.ErrFields(ctx, err)...)
			}
		}
		return nil
	})
}

// History returns all messages for a session ordered by time.
func (s *MySQLStore) History(ctx context.Context, sessionID string) ([]Message, error) {
	var rows []models.ChatMessage
	if err := s.db.WithContext(ctx).
		Where("session_uuid = ?", sessionID).
		Order("id").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}
	result := make([]Message, len(rows))
	for i, r := range rows {
		result[i] = Message{Role: r.Role, Content: r.Content, ID: r.UUID, Ts: r.CreatedAt}
	}
	return result, nil
}

// Exists checks whether a session exists.
func (s *MySQLStore) Exists(ctx context.Context, sessionID string) (bool, error) {
	var count int64
	if err := s.db.WithContext(ctx).
		Model(&models.ChatMessage{}).
		Where("session_uuid = ?", sessionID).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// Delete removes a session and its messages.
func (s *MySQLStore) Delete(ctx context.Context, sessionID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("session_uuid = ?", sessionID).Delete(&models.ChatMessage{}).Error; err != nil {
			return err
		}
		return tx.Where("uuid = ?", sessionID).Delete(&models.Session{}).Error
	})
}

// ListSessions returns sessions for a user, optionally filtered by agentID.
func (s *MySQLStore) ListSessions(ctx context.Context, userID int64, agentID string) ([]SessionInfo, error) {
	var sessions []models.Session
	q := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("ctime DESC")
	if agentID != "" {
		q = q.Where("agent_uuid = ?", agentID)
	}
	if err := q.Find(&sessions).Error; err != nil {
		return nil, err
	}
	result := make([]SessionInfo, 0, len(sessions))
	for _, s := range sessions {
		result = append(result, SessionInfo{
			ID:        s.UUID,
			Title:     s.Title,
			AgentID:   s.AgentUUID,
			CreatedAt: s.CreatedAt.Format(time.RFC3339),
		})
	}
	return result, nil
}

// SetAgent records which agent is used for a session, creating session record if new.
func (s *MySQLStore) SetAgent(ctx context.Context, sessionID, agentID string, userID int64) error {
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "uuid"}},
		DoUpdates: clause.AssignmentColumns([]string{"agent_uuid"}),
	}).Create(&models.Session{
		UUID:      sessionID,
		AgentUUID: agentID,
		UserID:    userID,
	}).Error
}

// GetAgent returns the agent for a session.
func (s *MySQLStore) GetAgent(ctx context.Context, sessionID string) (string, error) {
	var sess models.Session
	if err := s.db.WithContext(ctx).
		Where("uuid = ?", sessionID).
		First(&sess).Error; err != nil {
		return "", err
	}
	return sess.AgentUUID, nil
}

// Close is a no-op; the shared DB connection is managed by main.go.
func (s *MySQLStore) Close() error {
	return nil
}
