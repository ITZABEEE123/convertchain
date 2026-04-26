package main

import (
	"context"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"convert-chain/go-engine/internal/api"
	"convert-chain/go-engine/internal/api/handlers"
	appcrypto "convert-chain/go-engine/internal/crypto"
	"convert-chain/go-engine/internal/exchange"
	binanceclient "convert-chain/go-engine/internal/exchange/binance"
	bybitclient "convert-chain/go-engine/internal/exchange/bybit"
	graphclient "convert-chain/go-engine/internal/graph"
	"convert-chain/go-engine/internal/kyc"
	smileidclient "convert-chain/go-engine/internal/kyc/smileid"
	sumsubclient "convert-chain/go-engine/internal/kyc/sumsub"
	"convert-chain/go-engine/internal/pricing"
	"convert-chain/go-engine/internal/service"
	"convert-chain/go-engine/internal/statemachine"
	vaultclient "convert-chain/go-engine/internal/vault"
	"convert-chain/go-engine/internal/workers"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type dbHealth struct {
	pool *pgxpool.Pool
}

func (d dbHealth) PingContext(ctx context.Context) error {
	return d.pool.Ping(ctx)
}

type cacheHealth struct {
	client *redis.Client
}

func (c cacheHealth) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

func main() {
	logger := slog.Default()
	environment := strings.ToLower(strings.TrimSpace(os.Getenv("ENVIRONMENT")))
	if environment == "" {
		environment = "development"
	}

	dbURL := strings.TrimSpace(os.Getenv("DB_URL"))
	if dbURL == "" {
		logger.Error("DB_URL is required")
		os.Exit(1)
	}

	serviceToken := firstNonEmpty(strings.TrimSpace(os.Getenv("SERVICE_TOKEN")), "your-service-token")
	adminToken := strings.TrimSpace(os.Getenv("ADMIN_API_TOKEN"))
	redisURL := firstNonEmpty(strings.TrimSpace(os.Getenv("REDIS_URL")), "redis://:DevPassword123!@127.0.0.1:6379/0")
	vaultAddr := firstNonEmpty(strings.TrimSpace(os.Getenv("VAULT_ADDR")), "http://127.0.0.1:8200")
	vaultToken := strings.TrimSpace(os.Getenv("VAULT_TOKEN"))

	if keyName := detectedPrivateKeyEnv(); keyName != "" {
		logger.Error("private key material must not be loaded from environment", "env", keyName)
		os.Exit(1)
	}

	binanceCreds := map[string]string{}
	bybitCreds := map[string]string{}
	graphCreds := map[string]string{}
	smileIDCreds := map[string]string{}
	sumsubCreds := map[string]string{}
	var encryptor *appcrypto.PIIEncryptor

	if vaultToken == "" {
		logger.Warn("VAULT_TOKEN not set; using env-only local startup")
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		vault := vaultclient.New(vaultAddr, vaultToken, "secret")
		binanceCreds = optionalSecretMap(ctx, vault, "convertchain/binance", logger)
		bybitCreds = optionalSecretMap(ctx, vault, "convertchain/bybit", logger)
		graphCreds = optionalSecretMap(ctx, vault, "convertchain/graph", logger)
		smileIDCreds = optionalSecretMap(ctx, vault, "convertchain/smileid", logger)
		sumsubCreds = optionalSecretMap(ctx, vault, "convertchain/sumsub", logger)

		if token := optionalSecret(ctx, vault, "convertchain/service_token", "token", logger); token != "" {
			serviceToken = token
		}
		if token := optionalSecret(ctx, vault, "convertchain/admin_token", "token", logger); token != "" {
			adminToken = token
		}

		if piiKeyHex := optionalSecret(ctx, vault, "convertchain/pii_key", "key", logger); piiKeyHex != "" {
			piiKeyBytes, err := hex.DecodeString(piiKeyHex)
			if err != nil {
				logger.Warn("failed to decode vault PII key; continuing without app-level encryption", "error", err)
			} else {
				encryptor, err = appcrypto.NewPIIEncryptor(piiKeyBytes)
				if err != nil {
					logger.Warn("failed to initialize PII encryptor from vault key", "error", err)
				}
			}
		}
	}

	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		logger.Error("failed to create postgres pool", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(context.Background()); err != nil {
		logger.Error("failed to reach postgres", "error", err)
		os.Exit(1)
	}

	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		logger.Error("failed to parse REDIS_URL", "error", err)
		os.Exit(1)
	}

	redisClient := redis.NewClient(redisOpts)
	defer redisClient.Close()

	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		logger.Error("failed to reach redis", "error", err)
		os.Exit(1)
	}

	binanceClient := binanceclient.NewClient(
		firstNonEmpty(strings.TrimSpace(os.Getenv("BINANCE_API_KEY")), binanceCreds["api_key"]),
		firstNonEmpty(strings.TrimSpace(os.Getenv("BINANCE_API_SECRET")), strings.TrimSpace(os.Getenv("BINANCE_SECRET_KEY")), binanceCreds["api_secret"]),
		envBool("BINANCE_USE_TESTNET", true),
	)

	bybitClient := bybitclient.NewClient(
		firstNonEmpty(strings.TrimSpace(os.Getenv("BYBIT_API_KEY")), bybitCreds["api_key"]),
		firstNonEmpty(strings.TrimSpace(os.Getenv("BYBIT_API_SECRET")), strings.TrimSpace(os.Getenv("BYBIT_SECRET_KEY")), bybitCreds["api_secret"]),
		envBool("BYBIT_USE_TESTNET", true),
	)
	bybitFallbackEnabled := envBool("BYBIT_FALLBACK_ENABLED", false)
	if !bybitFallbackEnabled {
		logger.Info("bybit fallback disabled; primary exchange only", "env", "BYBIT_FALLBACK_ENABLED")
	}

	graphClient := graphclient.NewClient(
		firstNonEmpty(strings.TrimSpace(os.Getenv("GRAPH_API_KEY")), graphCreds["api_key"]),
		envBool("GRAPH_USE_SANDBOX", true),
	)

	sumsubUseSandbox := envBool("SUMSUB_USE_SANDBOX", environment != "production")
	if environment == "production" && sumsubUseSandbox {
		logger.Error("SUMSUB_USE_SANDBOX must be false in production")
		os.Exit(1)
	}

	smileIDPartnerID := firstNonEmpty(strings.TrimSpace(os.Getenv("SMILE_ID_PARTNER_ID")), smileIDCreds["partner_id"])
	smileIDAPIKey := firstNonEmpty(strings.TrimSpace(os.Getenv("SMILE_ID_API_KEY")), smileIDCreds["api_key"])
	sumsubAppToken := firstNonEmpty(strings.TrimSpace(os.Getenv("SUMSUB_APP_TOKEN")), sumsubCreds["app_token"])
	sumsubSecretKey := firstNonEmpty(strings.TrimSpace(os.Getenv("SUMSUB_SECRET_KEY")), sumsubCreds["secret_key"])
	sumsubWebhookSecret := firstNonEmpty(strings.TrimSpace(os.Getenv("SUMSUB_WEBHOOK_SECRET")), sumsubCreds["webhook_secret"], sumsubSecretKey)
	sumsubWebhookPublicBaseURL := firstNonEmpty(strings.TrimSpace(os.Getenv("SUMSUB_WEBHOOK_PUBLIC_BASE_URL")), sumsubCreds["webhook_public_base_url"])
	graphWebhookSecret := firstNonEmpty(strings.TrimSpace(os.Getenv("GRAPH_WEBHOOK_SECRET")), graphCreds["webhook_secret"])
	graphWebhookPublicBaseURL := firstNonEmpty(strings.TrimSpace(os.Getenv("GRAPH_WEBHOOK_PUBLIC_BASE_URL")), graphCreds["webhook_public_base_url"])
	kycPrimaryProvider := normalizeKYCPrimaryProvider(firstNonEmpty(strings.TrimSpace(os.Getenv("KYC_PRIMARY_PROVIDER")), defaultKYCPrimaryProvider(sumsubAppToken, sumsubSecretKey)))
	sumsubWebSDKLinkTTLSeconds := envInt("SUMSUB_WEBSDK_LINK_TTL_SECONDS", 1800)
	autoApproveKYC := envBool("AUTO_APPROVE_KYC", environment != "production")
	if environment == "production" && autoApproveKYC {
		logger.Warn("AUTO_APPROVE_KYC requested in production; forcing disabled")
		autoApproveKYC = false
	}

	var pricingFallback exchange.ExchangeClient
	if bybitFallbackEnabled {
		pricingFallback = bybitClient
	}
	pricingEngine := pricing.NewPricingEngine(binanceClient, pricingFallback, graphClient, redisClient, logger)
	appService := service.NewApplicationService(
		pool,
		pricingEngine,
		graphClient,
		binanceClient,
		pricingFallback,
		encryptor,
		logger,
		service.Options{
			AutoApproveKYC:             autoApproveKYC,
			Environment:                environment,
			KYCPrimaryProvider:         kycPrimaryProvider,
			SmileIDPartnerID:           smileIDPartnerID,
			SmileIDAPIKey:              smileIDAPIKey,
			SumsubWebhookSecret:        sumsubWebhookSecret,
			SumsubWebhookPublicBaseURL: sumsubWebhookPublicBaseURL,
			SumsubUseSandbox:           sumsubUseSandbox,
			SumsubTier1LevelName:       envWithDevDefault("SUMSUB_TIER1_LEVEL_NAME", "telegram-tier1", environment),
			SumsubTier2LevelName:       envWithDevDefault("SUMSUB_TIER2_LEVEL_NAME", "telegram-tier2", environment),
			SumsubTier3LevelName:       envWithDevDefault("SUMSUB_TIER3_LEVEL_NAME", "telegram-tier3", environment),
			SumsubTier4LevelName:       envWithDevDefault("SUMSUB_TIER4_LEVEL_NAME", "telegram-tier4", environment),
			SumsubWebSDKLinkTTLSeconds: sumsubWebSDKLinkTTLSeconds,
			GraphWebhookSecret:         graphWebhookSecret,
			GraphWebhookPublicBaseURL:  graphWebhookPublicBaseURL,
			BybitFallbackEnabled:       bybitFallbackEnabled,
		},
	)

	var smileIDClient *smileidclient.Client
	if smileIDPartnerID != "" && smileIDAPIKey != "" {
		smileIDClient = smileidclient.NewClient(smileIDPartnerID, smileIDAPIKey, envBool("SMILE_ID_USE_SANDBOX", true))
	} else if smileIDPartnerID != "" || smileIDAPIKey != "" {
		logger.Warn("smile id credentials incomplete; tier 1 KYC provider disabled")
	}

	var sumsubClient *sumsubclient.Client
	if sumsubAppToken != "" && sumsubSecretKey != "" {
		sumsubClient = sumsubclient.NewClient(sumsubAppToken, sumsubSecretKey, sumsubUseSandbox)
	} else if sumsubAppToken != "" || sumsubSecretKey != "" {
		logger.Warn("sumsub credentials incomplete; advanced KYC provider disabled")
	}

	if smileIDClient != nil || sumsubClient != nil {
		appService.SetKYCOrchestrator(kyc.NewKYCOrchestrator(smileIDClient, sumsubClient, appService, logger))
	}

	redisAdapter := service.NewRedisAdapter(redisClient)
	workerCtx, workerCancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer workerCancel()

	depositPolicies := workers.NewDepositPolicySetFromEnv()
	blockchainClient := service.NewBlockchainClientFromEnv(logger)
	depositWatcher := workers.NewDepositWatcherWithPolicy(
		appService,
		blockchainClient,
		statemachine.NewTradeFSM(),
		depositPolicies,
		5*time.Second,
		logger,
	)
	depositBackfill := workers.NewDepositBackfillScanner(
		appService,
		blockchainClient,
		depositPolicies,
		30*time.Second,
		logger,
	)
	quoteExpiry := workers.NewQuoteExpiryWorker(appService, 15*time.Second, logger)
	conversionProcessor := workers.NewConversionProcessor(
		appService,
		service.NewExchangeSandboxConversionClient(binanceClient, pricingFallback, logger),
		5*time.Second,
		logger,
	)
	payoutProcessor := workers.NewPayoutProcessor(
		appService,
		service.NewGraphSandboxPayoutClient(appService, graphClient, logger),
		5*time.Second,
		logger,
	)

	go depositWatcher.Run(workerCtx)
	go depositBackfill.Run(workerCtx)
	go quoteExpiry.Run(workerCtx)
	go conversionProcessor.Run(workerCtx)
	go payoutProcessor.Run(workerCtx)

	router := api.NewRouter(api.RouterConfig{
		ServiceToken:           serviceToken,
		AdminToken:             adminToken,
		Logger:                 logger,
		RedisClient:            redisAdapter,
		HealthHandler:          handlers.NewHealthHandler(dbHealth{pool: pool}, cacheHealth{client: redisClient}),
		UserHandler:            handlers.NewUserHandler(appService),
		SecurityHandler:        handlers.NewSecurityHandler(appService),
		AccountHandler:         handlers.NewAccountHandler(appService),
		KYCHandler:             handlers.NewKYCHandler(appService),
		QuoteHandler:           handlers.NewQuoteHandler(appService),
		TradeHandler:           handlers.NewTradeHandler(appService),
		BankHandler:            handlers.NewBankHandler(appService),
		DisputeHandler:         handlers.NewDisputeHandler(appService),
		AdminHandler:           handlers.NewAdminHandler(appService),
		NotificationHandler:    handlers.NewNotificationHandler(appService),
		ProviderWebhookHandler: handlers.NewProviderWebhookHandler(appService),
	})

	srv := &http.Server{
		Addr:    ":9000",
		Handler: router,
	}
	go func() {
		<-workerCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("graceful shutdown failed", "error", err)
		}
	}()

	logger.Info(
		"convertchain go engine ready",
		"addr", ":9000",
		"environment", environment,
		"auto_approve_kyc", autoApproveKYC,
		"trade_create_endpoint_mode", strings.ToLower(strings.TrimSpace(os.Getenv("TRADE_CREATE_ENDPOINT_MODE"))),
		"graph_webhook_event_id_mode", strings.ToLower(strings.TrimSpace(os.Getenv("GRAPH_WEBHOOK_EVENT_ID_MODE"))),
		"graph_sandbox", graphClient.IsSandbox(),
		"graph_webhook_secret_configured", graphWebhookSecret != "",
		"kyc_primary_provider", kycPrimaryProvider,
		"smile_id_configured", smileIDClient != nil,
		"sumsub_configured", sumsubClient != nil,
		"sumsub_sandbox", sumsubUseSandbox,
		"sumsub_webhook_secret_configured", sumsubWebhookSecret != "",
		"bybit_fallback_enabled", bybitFallbackEnabled,
	)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped unexpectedly", "error", err)
		os.Exit(1)
	}
}

func detectedPrivateKeyEnv() string {
	for _, key := range []string{
		"BTC_PRIVATE_KEY",
		"ETH_PRIVATE_KEY",
		"USDC_PRIVATE_KEY",
		"WALLET_PRIVATE_KEY",
		"SECP256K1_PRIVATE_KEY",
	} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return key
		}
	}
	return ""
}

func optionalSecret(ctx context.Context, vault *vaultclient.Client, path, field string, logger *slog.Logger) string {
	value, err := vault.GetSecret(ctx, path, field)
	if err != nil {
		logger.Warn("vault secret unavailable; continuing with env fallback", "path", path, "field", field, "error", err)
		return ""
	}
	return strings.TrimSpace(value)
}

func optionalSecretMap(ctx context.Context, vault *vaultclient.Client, path string, logger *slog.Logger) map[string]string {
	values, err := vault.GetSecretMap(ctx, path)
	if err != nil {
		logger.Warn("vault secret map unavailable; continuing with env fallback", "path", path, "error", err)
		return map[string]string{}
	}
	return values
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func envWithDevDefault(key, fallback, environment string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value != "" {
		return value
	}
	if strings.EqualFold(strings.TrimSpace(environment), "production") {
		return ""
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}

	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func defaultKYCPrimaryProvider(sumsubAppToken, sumsubSecretKey string) string {
	if strings.TrimSpace(sumsubAppToken) != "" && strings.TrimSpace(sumsubSecretKey) != "" {
		return "sumsub"
	}
	return "smile_id"
}

func normalizeKYCPrimaryProvider(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "sumsub":
		return "sumsub"
	case "smileid", "smile_id", "smile-id":
		return "smile_id"
	default:
		return "smile_id"
	}
}
