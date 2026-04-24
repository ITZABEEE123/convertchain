package middleware

import (
	"crypto/subtle"
	"net/http"

	"convert-chain/go-engine/internal/api/dto"

	"github.com/gin-gonic/gin"
)

// ServiceTokenAuth validates the X-Service-Token header using constant-time
// comparison to prevent timing attacks.
func ServiceTokenAuth(expectedToken string) gin.HandlerFunc {
	return tokenAuth("X-Service-Token", expectedToken, "Invalid service token")
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
