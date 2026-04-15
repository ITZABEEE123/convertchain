package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"convert-chain/go-engine/internal/api/dto"

	"github.com/gin-gonic/gin"
)

// RedisClient defines the Redis operations needed by the rate limiter.
type RedisClient interface {
	ZAdd(ctx context.Context, key string, score float64, member string) error
	ZRemRangeByScore(ctx context.Context, key string, min, max string) error
	ZCard(ctx context.Context, key string) (int64, error)
	Expire(ctx context.Context, key string, ttl time.Duration) error
}

type RateLimiterConfig struct {
	KeyPrefix string        // e.g. "rl:quotes:" — separates different limiters
	Limit     int64         // max requests allowed in the window
	Window    time.Duration // sliding window size
}

// SlidingWindowRateLimiter returns a Gin middleware enforcing a sliding window rate limit.
//
// Usage in router.go:
//
//	quoteLimiter := middleware.SlidingWindowRateLimiter(redis, middleware.RateLimiterConfig{
//	    KeyPrefix: "rl:quotes:", Limit: 10, Window: time.Minute,
//	})
//	v1.POST("/quotes", quoteLimiter, cfg.QuoteHandler.CreateQuote)
func SlidingWindowRateLimiter(redis RedisClient, config RateLimiterConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		identifier := c.ClientIP() // rate-limit by IP; use user_id in handler for user-level limits

		key := config.KeyPrefix + identifier
		now := time.Now()
		nowUnix := float64(now.UnixNano())
		windowStart := float64(now.Add(-config.Window).UnixNano())

		// Add current request timestamp as both score and member.
		if err := redis.ZAdd(ctx, key, nowUnix, fmt.Sprintf("%d", now.UnixNano())); err != nil {
			c.Next() // fail open if Redis is down
			return
		}

		// Remove requests outside the window.
		if err := redis.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("%f", windowStart)); err != nil {
			c.Next()
			return
		}

		// Keep the key alive for one window duration.
		_ = redis.Expire(ctx, key, config.Window)

		// Count current requests in window.
		count, err := redis.ZCard(ctx, key)
		if err != nil {
			c.Next()
			return
		}

		if count > config.Limit {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, dto.NewError(
				dto.ErrCodeRateLimited,
				fmt.Sprintf("Rate limit exceeded: max %d requests per %s", config.Limit, config.Window),
				nil,
			))
			return
		}

		c.Next()
	}
}
