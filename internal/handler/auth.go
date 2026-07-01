package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"ansmeee-ai-agent/internal/middleware"
	"ansmeee-ai-agent/internal/models"
	"ansmeee-ai-agent/internal/tracing"
	"ansmeee-ai-agent/pkg/logger"
	"ansmeee-ai-agent/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const jwtSecretDefault = "ai-agent-secret-key-change-in-production"

// AuthHandler handles user registration and login.
type AuthHandler struct {
	db        *gorm.DB
	jwtSecret string
}

// NewAuthHandler creates a new auth handler.
func NewAuthHandler(db *gorm.DB, jwtSecret string) *AuthHandler {
	if jwtSecret == "" {
		jwtSecret = jwtSecretDefault
	}
	return &AuthHandler{db: db, jwtSecret: jwtSecret}
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Register creates a new user account.
func (h *AuthHandler) Register(c *gin.Context) {
	ctx := c.Request.Context()
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Email == "" || req.Password == "" {
		response.BadRequest(c, "email and password are required")
		return
	}
	if len(req.Password) < 6 {
		response.BadRequest(c, "password must be at least 6 characters")
		return
	}

	// Check if user already exists.
	var existing models.User
	err := h.db.WithContext(ctx).Where("email = ?", req.Email).First(&existing).Error
	if err == nil {
		response.Fail(c, http.StatusConflict, response.CodeBadRequest, "email already registered")
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.L().Error("check email uniqueness failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, "database error")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		logger.L().Error("hash password failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, "failed to hash password")
		return
	}

	user := models.User{
		UUID:     strings.ReplaceAll(uuid.New().String(), "-", ""),
		Email:    req.Email,
		Password: string(hash),
		Status:   1,
	}
	if err := h.db.WithContext(ctx).Create(&user).Error; err != nil {
		logger.L().Error("create user failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, "failed to create user")
		return
	}

	token, err := h.generateJWT(user.ID, user.UUID)
	if err != nil {
		logger.L().Error("generate JWT failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, "failed to generate token")
		return
	}

	response.OK(c, gin.H{
		"token":   token,
		"user_id": user.UUID,
		"email":   user.Email,
	})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Login authenticates a user and returns a JWT.
func (h *AuthHandler) Login(c *gin.Context) {
	ctx := c.Request.Context()
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Email == "" || req.Password == "" {
		response.BadRequest(c, "email and password are required")
		return
	}

	var user models.User
	err := h.db.WithContext(ctx).Where("email = ?", req.Email).First(&user).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			logger.L().Error("query user failed", tracing.ErrFields(ctx, err)...)
		}
		response.Fail(c, http.StatusUnauthorized, response.CodeUnauthorized, "invalid email or password")
		return
	}
	if user.Status != 1 {
		response.Fail(c, http.StatusUnauthorized, response.CodeUnauthorized, "account is disabled")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		response.Fail(c, http.StatusUnauthorized, response.CodeUnauthorized, "invalid email or password")
		return
	}

	token, err := h.generateJWT(user.ID, user.UUID)
	if err != nil {
		logger.L().Error("generate JWT failed", tracing.ErrFields(ctx, err)...)
		response.InternalError(c, "failed to generate token")
		return
	}

	response.OK(c, gin.H{
		"token":   token,
		"user_id": user.UUID,
		"email":   user.Email,
	})
}

// Me returns the current user info.
func (h *AuthHandler) Me(c *gin.Context) {
	userID := c.GetInt64(middleware.CtxUserID)
	userUUID := c.GetString(middleware.CtxUserUUID)
	response.OK(c, gin.H{
		"user_id": userUUID,
		"id":      userID,
	})
}

func (h *AuthHandler) generateJWT(userID int64, userUUID string) (string, error) {
	claims := jwt.MapClaims{
		"user_id":   userID,
		"user_uuid": userUUID,
		"exp":       time.Now().Add(7 * 24 * time.Hour).Unix(),
		"iat":       time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(h.jwtSecret))
}
