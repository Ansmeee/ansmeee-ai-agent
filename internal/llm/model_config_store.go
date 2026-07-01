package llm

import (
	"context"
	"fmt"

	"ansmeee-ai-agent/internal/models"
	"ansmeee-ai-agent/internal/tracing"
	"ansmeee-ai-agent/pkg/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ModelConfigStore persists user model configurations.
type ModelConfigStore struct {
	db *gorm.DB
}

// NewModelConfigStore creates a new model config store.
func NewModelConfigStore(db *gorm.DB) *ModelConfigStore {
	return &ModelConfigStore{db: db}
}

// GetByUserAndType returns the model config for a user and model type, or nil if not set.
func (s *ModelConfigStore) GetByUserAndType(ctx context.Context, userID int64, modelType int8) (*models.ModelConfig, error) {
	var cfg models.ModelConfig
	err := s.db.WithContext(ctx).Where("user_id = ? AND model_type = ?", userID, modelType).First(&cfg).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		logger.L().Error("query model config failed", append(tracing.ErrFields(ctx, err), zap.Int64("user_id", userID))...)
		return nil, fmt.Errorf("query model config: %w", err)
	}
	return &cfg, nil
}

// GetByUser returns all model configs for a user.
func (s *ModelConfigStore) GetByUser(ctx context.Context, userID int64) ([]*models.ModelConfig, error) {
	var cfgs []*models.ModelConfig
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).Find(&cfgs).Error
	if err != nil {
		logger.L().Error("list model configs failed", append(tracing.ErrFields(ctx, err), zap.Int64("user_id", userID))...)
		return nil, fmt.Errorf("list model configs: %w", err)
	}
	return cfgs, nil
}

// Save upserts the model config for a user and model type.
func (s *ModelConfigStore) Save(ctx context.Context, userID int64, modelType int8, model, baseURL, token string) (*models.ModelConfig, error) {
	cfg := models.ModelConfig{
		UserID:    userID,
		ModelType: modelType,
		Model:     model,
		BaseURL:   baseURL,
		Token:     token,
	}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "model_type"}},
		DoUpdates: clause.AssignmentColumns([]string{"model", "base_url", "token"}),
	}).Create(&cfg).Error
	if err != nil {
		logger.L().Error("save model config failed", append(tracing.ErrFields(ctx, err), zap.Int64("user_id", userID))...)
		return nil, fmt.Errorf("save model config: %w", err)
	}
	return s.GetByUserAndType(ctx, userID, modelType)
}
