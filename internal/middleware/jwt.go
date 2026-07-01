package middleware

import (
	"net/http"
	"strings"

	"ansmeee-ai-agent/pkg/logger"
	"ansmeee-ai-agent/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

const (
	CtxUserID    = "user_id"
	CtxUserUUID  = "user_uuid"
	CtxUserEmail = "user_email"
)

// JWTAuth validates JWT tokens for protected routes.
func JWTAuth(secret string) gin.HandlerFunc {
	secretBytes := []byte(secret)
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			response.Fail(c, http.StatusUnauthorized, response.CodeUnauthorized, "missing authorization header")
			c.Abort()
			return
		}

		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			response.Fail(c, http.StatusUnauthorized, response.CodeUnauthorized, "invalid authorization format")
			c.Abort()
			return
		}

		token, err := jwt.Parse(parts[1], func(t *jwt.Token) (interface{}, error) {
			return secretBytes, nil
		})
		if err != nil || !token.Valid {
			logger.L().Warn("JWT auth failed", zap.Error(err))
			response.Fail(c, http.StatusUnauthorized, response.CodeUnauthorized, "invalid or expired token")
			c.Abort()
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			logger.L().Warn("JWT claims type assertion failed")
			response.Fail(c, http.StatusUnauthorized, response.CodeUnauthorized, "invalid token claims")
			c.Abort()
			return
		}

		userID, _ := claims["user_id"].(float64)
		userUUID, _ := claims["user_uuid"].(string)
		userEmail, _ := claims["user_email"].(string)
		c.Set(CtxUserID, int64(userID))
		c.Set(CtxUserUUID, userUUID)
		c.Set(CtxUserEmail, userEmail)
		c.Next()
	}
}
