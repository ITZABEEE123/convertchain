package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type DBPinger interface {
	PingContext(ctx context.Context) error
}

type CachePinger interface {
	Ping(ctx context.Context) error
}

type HealthHandler struct {
	db    DBPinger
	cache CachePinger
}

func NewHealthHandler(db DBPinger, cache CachePinger) *HealthHandler {
	return &HealthHandler{db: db, cache: cache}
}

// Health handles GET /health — liveness probe.
func (h *HealthHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"service":   "convertchain-go-engine",
	})
}

// Ready handles GET /ready — readiness probe.
// Returns 503 if any dependency is unreachable.
func (h *HealthHandler) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	dbOK := h.db.PingContext(ctx) == nil
	cacheOK := h.cache.Ping(ctx) == nil

	status := "ok"
	httpStatus := http.StatusOK

	if !dbOK || !cacheOK {
		status = "degraded"
		httpStatus = http.StatusServiceUnavailable
	}

	c.JSON(httpStatus, gin.H{
		"status":    status,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"checks": gin.H{
			"database": boolToStatus(dbOK),
			"cache":    boolToStatus(cacheOK),
		},
	})
}

func boolToStatus(ok bool) string {
	if ok {
		return "ok"
	}
	return "unreachable"
}