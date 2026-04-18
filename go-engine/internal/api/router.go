package api

import (
	"log/slog"
	"time"

	"convert-chain/go-engine/internal/api/middleware"

	"github.com/gin-gonic/gin"

	"convert-chain/go-engine/internal/api/handlers"
)

type RouterConfig struct {
	ServiceToken   string
	Logger         *slog.Logger
	RedisClient    middleware.RedisClient
	HealthHandler  *handlers.HealthHandler
	UserHandler    *handlers.UserHandler
	KYCHandler     *handlers.KYCHandler
	QuoteHandler   *handlers.QuoteHandler
	TradeHandler   *handlers.TradeHandler
	BankHandler    *handlers.BankHandler
	DisputeHandler *handlers.DisputeHandler
}

// NewRouter creates and configures the Gin engine with the handlers used by
// the HTTP API.
func NewRouter(cfg RouterConfig) *gin.Engine {
	engine := gin.New()

	engine.Use(middleware.PanicRecovery(cfg.Logger))
	engine.Use(middleware.RequestLogger(cfg.Logger))

	if cfg.HealthHandler != nil {
		engine.GET("/health", cfg.HealthHandler.Health)
		engine.GET("/ready", cfg.HealthHandler.Ready)
	}

	v1 := engine.Group("/api/v1")
	v1.Use(middleware.ServiceTokenAuth(cfg.ServiceToken))
	{
		var quoteLimiter gin.HandlerFunc
		var tradeLimiter gin.HandlerFunc
		if cfg.RedisClient != nil {
			quoteLimiter = middleware.SlidingWindowRateLimiter(cfg.RedisClient, middleware.RateLimiterConfig{
				KeyPrefix: "rl:quotes:",
				Limit:     10,
				Window:    time.Minute,
			})
			tradeLimiter = middleware.SlidingWindowRateLimiter(cfg.RedisClient, middleware.RateLimiterConfig{
				KeyPrefix: "rl:trades:",
				Limit:     5,
				Window:    time.Minute,
			})
		}

		if cfg.UserHandler != nil {
			v1.POST("/users", cfg.UserHandler.CreateOrGetUser)
			v1.POST("/consent", cfg.UserHandler.RecordConsent)
		}

		if cfg.KYCHandler != nil {
			v1.POST("/kyc/submit", cfg.KYCHandler.SubmitKYC)
			v1.GET("/kyc/status/:user_id", cfg.KYCHandler.GetKYCStatus)
		}

		if cfg.QuoteHandler != nil {
			if quoteLimiter != nil {
				v1.POST("/quotes", quoteLimiter, cfg.QuoteHandler.CreateQuote)
			} else {
				v1.POST("/quotes", cfg.QuoteHandler.CreateQuote)
			}
		}

		if cfg.TradeHandler != nil {
			if tradeLimiter != nil {
				v1.POST("/trades", tradeLimiter, cfg.TradeHandler.CreateTrade)
			} else {
				v1.POST("/trades", cfg.TradeHandler.CreateTrade)
			}
			v1.GET("/trades/:trade_id", cfg.TradeHandler.GetTrade)
		}

		if cfg.BankHandler != nil {
			v1.GET("/banks", cfg.BankHandler.ListBanks)
			v1.POST("/bank-accounts/resolve", cfg.BankHandler.ResolveBankAccount)
			v1.POST("/bank-accounts", cfg.BankHandler.AddBankAccount)
			v1.GET("/bank-accounts/:user_id", cfg.BankHandler.ListBankAccounts)
		}

		if cfg.DisputeHandler != nil {
			v1.POST("/disputes", cfg.DisputeHandler.RaiseDispute)
		}
	}

	return engine
}
