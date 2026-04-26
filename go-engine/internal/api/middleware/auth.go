package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"convert-chain/go-engine/internal/api/dto"

	"github.com/gin-gonic/gin"
)

// ServiceTokenAuth validates the X-Service-Token header using constant-time
// comparison to prevent timing attacks. X-Service-Token remains canonical;
// Authorization: Bearer is accepted for platform clients that only support it.
func ServiceTokenAuth(expectedToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("X-Service-Token")
		if token == "" {
			token = bearerToken(c.GetHeader("Authorization"))
		}

		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, dto.NewError(
				dto.ErrCodeUnauthorized, "X-Service-Token header or Authorization Bearer token is required", nil,
			))
			return
		}

		if subtle.ConstantTimeCompare([]byte(token), []byte(expectedToken)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, dto.NewError(
				dto.ErrCodeUnauthorized, "Invalid service token", nil,
			))
			return
		}

		c.Next()
	}
}

// AdminTokenAuth validates the X-Admin-Token header using constant-time
// comparison to protect admin-only endpoints.
func AdminTokenAuth(expectedToken string) gin.HandlerFunc {
	return tokenAuth("X-Admin-Token", expectedToken, "Invalid admin token")
}

func tokenAuth(headerName string, expectedToken string, invalidMessage string) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader(headerName)

		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, dto.NewError(
				dto.ErrCodeUnauthorized, headerName+" header is required", nil,
			))
			return
		}

		// IMPORTANT: crypto/subtle.ConstantTimeCompare prevents timing attacks.
		// A naive `token == expectedToken` leaks information about the secret
		// based on how fast the comparison returns. ConstantTimeCompare always
		// takes the same time regardless of how many bytes match.
		if subtle.ConstantTimeCompare([]byte(token), []byte(expectedToken)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, dto.NewError(
				dto.ErrCodeUnauthorized, invalidMessage, nil,
			))
			return
		}

		c.Next()
	}
}

func bearerToken(header string) string {
	trimmed := strings.TrimSpace(header)
	if trimmed == "" {
		return ""
	}
	prefix := "Bearer "
	if len(trimmed) <= len(prefix) || !strings.EqualFold(trimmed[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(trimmed[len(prefix):])
}
