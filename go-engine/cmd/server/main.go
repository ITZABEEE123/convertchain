package main

import (
	"context"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"convert-chain/go-engine/internal/api"
	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/api/handlers"
	"convert-chain/go-engine/internal/crypto"
	"convert-chain/go-engine/internal/domain"
	binanceclient "convert-chain/go-engine/internal/exchange/binance"
	bybitclient "convert-chain/go-engine/internal/exchange/bybit"
	graphclient "convert-chain/go-engine/internal/graph"
	smileidclient "convert-chain/go-engine/internal/kyc/smileid"
	vaultclient "convert-chain/go-engine/internal/vault"
)

type placeholderService struct{}

func (placeholderService) PingContext(context.Context) error { return nil }
func (placeholderService) Ping(context.Context) error        { return nil }

func (placeholderService) CreateOrGetUser(context.Context, dto.CreateUserRequest) (*domain.User, bool, error) {
	return nil, false, errors.New("user service not wired")
}

func (placeholderService) RecordConsent(context.Context, string, string, time.Time) error {
	return errors.New("user service not wired")
}

func (placeholderService) SubmitKYC(context.Context, dto.KYCSubmitRequest) error {
	return errors.New("kyc service not wired")
}

func (placeholderService) GetKYCStatus(context.Context, string) (*domain.KYCDocument, error) {
	return nil, errors.New("kyc service not wired")
}

func (placeholderService) CreateQuote(context.Context, dto.QuoteRequest) (*domain.Quote, error) {
	return nil, errors.New("quote service not wired")
}

func (placeholderService) GetUserKYCStatus(context.Context, string) (string, error) {
	return "NOT_STARTED", errors.New("service not wired")
}

func (placeholderService) CreateTrade(context.Context, dto.CreateTradeRequest) (*domain.Trade, error) {
	return nil, errors.New("trade service not wired")
}

func (placeholderService) GetTrade(context.Context, string) (*domain.Trade, error) {
	return nil, errors.New("trade service not wired")
}

func (placeholderService) AddBankAccount(context.Context, dto.AddBankAccountRequest) (*domain.BankAccount, error) {
	return nil, errors.New("bank service not wired")
}

func (placeholderService) ListBankAccounts(context.Context, string) ([]*domain.BankAccount, error) {
	return nil, errors.New("bank service not wired")
}

func (placeholderService) RaiseDispute(context.Context, dto.DisputeRequest) (*handlers.DisputeRecord, error) {
	return nil, errors.New("dispute service not wired")
}

func main() {
	logger := slog.Default()
	serviceToken := os.Getenv("SERVICE_TOKEN")
	if serviceToken == "" {
		// Dev fallback to simplify local API testing when Vault is not configured.
		serviceToken = "your-service-token"
	}

	vaultAddr := os.Getenv("VAULT_ADDR")
	if vaultAddr == "" {
		vaultAddr = "http://127.0.0.1:8200"
	}

	vaultToken := os.Getenv("VAULT_TOKEN")
	if vaultToken == "" {
		logger.Warn("VAULT_TOKEN not set; running without Vault bootstrap", "hint", "set VAULT_TOKEN to enable provider/PII secrets")
	} else {
		ctx := context.Background()
		vault := vaultclient.New(vaultAddr, vaultToken, "secret")

		binanceCreds, err := vault.GetSecretMap(ctx, "convertchain/binance")
		if err != nil {
			logger.Error("failed to load Binance creds", "error", err)
			os.Exit(1)
		}

		bybitCreds, err := vault.GetSecretMap(ctx, "convertchain/bybit")
		if err != nil {
			logger.Error("failed to load Bybit creds", "error", err)
			os.Exit(1)
		}

		graphCreds, err := vault.GetSecretMap(ctx, "convertchain/graph")
		if err != nil {
			logger.Error("failed to load Graph creds", "error", err)
			os.Exit(1)
		}

		smileCreds, err := vault.GetSecretMap(ctx, "convertchain/smileid")
		if err != nil {
			logger.Error("failed to load Smile ID creds", "error", err)
			os.Exit(1)
		}

		piiKeyHex, err := vault.GetSecret(ctx, "convertchain/pii_key", "key")
		if err != nil {
			logger.Error("failed to load PII key", "error", err)
			os.Exit(1)
		}

		piiKeyBytes, err := hex.DecodeString(piiKeyHex)
		if err != nil {
			logger.Error("failed to decode PII key", "error", err)
			os.Exit(1)
		}

		piiEncryptor, err := crypto.NewPIIEncryptor(piiKeyBytes)
		if err != nil {
			logger.Error("failed to initialize PII encryptor", "error", err)
			os.Exit(1)
		}

		vaultServiceToken, err := vault.GetSecret(ctx, "convertchain/service_token", "token")
		if err != nil {
			logger.Error("failed to load service token", "error", err)
			os.Exit(1)
		}
		if vaultServiceToken != "" {
			serviceToken = vaultServiceToken
		}

		binanceClient := binanceclient.NewClient(
			binanceCreds["api_key"],
			binanceCreds["api_secret"],
			os.Getenv("BINANCE_USE_TESTNET") == "true",
		)

		bybitClient := bybitclient.NewClient(
			bybitCreds["api_key"],
			bybitCreds["api_secret"],
			os.Getenv("BYBIT_USE_TESTNET") == "true",
		)

		graphClient := graphclient.NewClient(
			graphCreds["api_key"],
			os.Getenv("GRAPH_USE_SANDBOX") == "true",
		)

		smileIDClient := smileidclient.NewClient(
			smileCreds["partner_id"],
			smileCreds["api_key"],
			os.Getenv("SMILE_ID_USE_SANDBOX") == "true",
		)

		// Keep these referenced so the file compiles until the real service wiring uses them.
		_ = piiEncryptor
		_ = binanceClient
		_ = bybitClient
		_ = graphClient
		_ = smileIDClient
	}

	deps := placeholderService{}
	router := api.NewRouter(api.RouterConfig{
		ServiceToken:   serviceToken,
		Logger:         logger,
		HealthHandler:  handlers.NewHealthHandler(deps, deps),
		UserHandler:    handlers.NewUserHandler(deps),
		KYCHandler:     handlers.NewKYCHandler(deps),
		QuoteHandler:   handlers.NewQuoteHandler(deps),
		TradeHandler:   handlers.NewTradeHandler(deps),
		BankHandler:    handlers.NewBankHandler(deps),
		DisputeHandler: handlers.NewDisputeHandler(deps),
	})

	srv := &http.Server{Addr: ":9000", Handler: router}
	slog.Info("go engine listening", "addr", ":9000")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
