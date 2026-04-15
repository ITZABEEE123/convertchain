package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
	"convert-chain/go-engine/internal/api/dto"
)

// ServiceTokenAuth validates the X-Service-Token header using constant-time
// comparison to prevent timing attacks.
func ServiceTokenAuth(expectedToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("X-Service-Token")

		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, dto.NewError(
				dto.ErrCodeUnauthorized, "X-Service-Token header is required", nil,
			))
			return
		}

		// IMPORTANT: crypto/subtle.ConstantTimeCompare prevents timing attacks.
		// A naive `token == expectedToken` leaks information about the secret
		// based on how fast the comparison returns. ConstantTimeCompare always
		// takes the same time regardless of how many bytes match.
		if subtle.ConstantTimeCompare([]byte(token), []byte(expectedToken)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, dto.NewError(
				dto.ErrCodeUnauthorized, "Invalid service token", nil,
			))
			return
		}

		c.Next()
	}
}