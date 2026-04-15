package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"convert-chain/go-engine/internal/api/dto"
)

// PanicRecovery catches any panic in a handler and converts it to a 500 response.
// Without this, a single panicking handler would crash the entire server.
func PanicRecovery(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				stack := debug.Stack()
				logger.Error("handler panic recovered",
					"error", err,
					"method", c.Request.Method,
					"path", c.Request.URL.Path,
					"stack", string(stack),
				)
				c.AbortWithStatusJSON(http.StatusInternalServerError, dto.NewError(
					dto.ErrCodeInternalError, "An unexpected error occurred", nil,
				))
			}
		}()
		c.Next()
	}
}