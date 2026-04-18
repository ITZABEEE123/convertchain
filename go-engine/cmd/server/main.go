package main

import (
    "context"
    "encoding/hex"
    "errors"
    "log/slog"
    "net/http"
    "os"
    "os/signal"
    "strings"
    "syscall"
    "time"

    "convert-chain/go-engine/internal/api"
    "convert-chain/go-engine/internal/api/handlers"
    appcrypto "convert-chain/go-engine/internal/crypto"
    binanceclient "convert-chain/go-engine/internal/exchange/binance"
    bybitclient "convert-chain/go-engine/internal/exchange/bybit"
    graphclient "convert-chain/go-engine/internal/graph"
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

    dbURL := strings.TrimSpace(os.Getenv("DB_URL"))
    if dbURL == "" {
        logger.Error("DB_URL is required")
        os.Exit(1)
    }

    serviceToken := firstNonEmpty(strings.TrimSpace(os.Getenv("SERVICE_TOKEN")), "your-service-token")
    redisURL := firstNonEmpty(strings.TrimSpace(os.Getenv("REDIS_URL")), "redis://:DevPassword123!@127.0.0.1:6379/0")
    vaultAddr := firstNonEmpty(strings.TrimSpace(os.Getenv("VAULT_ADDR")), "http://127.0.0.1:8200")
    vaultToken := strings.TrimSpace(os.Getenv("VAULT_TOKEN"))

    binanceCreds := map[string]string{}
    bybitCreds := map[string]string{}
    graphCreds := map[string]string{}
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

        if token := optionalSecret(ctx, vault, "convertchain/service_token", "token", logger); token != "" {
            serviceToken = token
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

    graphClient := graphclient.NewClient(
        firstNonEmpty(strings.TrimSpace(os.Getenv("GRAPH_API_KEY")), graphCreds["api_key"]),
        envBool("GRAPH_USE_SANDBOX", true),
    )

    pricingEngine := pricing.NewPricingEngine(binanceClient, bybitClient, graphClient, redisClient, logger)
    appService := service.NewApplicationService(
        pool,
        pricingEngine,
        graphClient,
        encryptor,
        logger,
        service.Options{
            AutoApproveKYC: envBool("AUTO_APPROVE_KYC", true),
        },
    )

    redisAdapter := service.NewRedisAdapter(redisClient)
    workerCtx, workerCancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
    defer workerCancel()

    depositWatcher := workers.NewDepositWatcher(
        appService,
        service.NewSandboxBlockchainClient(),
        statemachine.NewTradeFSM(),
        5*time.Second,
        logger,
    )
    quoteExpiry := workers.NewQuoteExpiryWorker(appService, 15*time.Second, logger)
    payoutProcessor := workers.NewPayoutProcessor(
        appService,
        service.NewGraphSandboxPayoutClient(appService, graphClient, logger),
        5*time.Second,
        logger,
    )

    go depositWatcher.Run(workerCtx)
    go quoteExpiry.Run(workerCtx)
    go payoutProcessor.Run(workerCtx)

    router := api.NewRouter(api.RouterConfig{
        ServiceToken:   serviceToken,
        Logger:         logger,
        RedisClient:    redisAdapter,
        HealthHandler:  handlers.NewHealthHandler(dbHealth{pool: pool}, cacheHealth{client: redisClient}),
        UserHandler:    handlers.NewUserHandler(appService),
        KYCHandler:     handlers.NewKYCHandler(appService),
        QuoteHandler:   handlers.NewQuoteHandler(appService),
        TradeHandler:   handlers.NewTradeHandler(appService),
        BankHandler:    handlers.NewBankHandler(appService),
        DisputeHandler: handlers.NewDisputeHandler(appService),
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
        "auto_approve_kyc", envBool("AUTO_APPROVE_KYC", true),
        "graph_sandbox", graphClient.IsSandbox(),
    )

    if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
        logger.Error("server stopped unexpectedly", "error", err)
        os.Exit(1)
    }
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
