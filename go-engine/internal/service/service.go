package service

import (
	"log/slog"

	appcrypto "convert-chain/go-engine/internal/crypto"
	"convert-chain/go-engine/internal/exchange"
	graphclient "convert-chain/go-engine/internal/graph"
	"convert-chain/go-engine/internal/kyc"
	"convert-chain/go-engine/internal/pricing"
	"convert-chain/go-engine/internal/statemachine"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Options struct {
	AutoApproveKYC           bool
	Environment              string
	KYCPrimaryProvider       string
	SmileIDPartnerID         string
	SmileIDAPIKey            string
	SumsubWebhookSecret      string
	SumsubWebhookPublicBaseURL string
	SumsubUseSandbox         bool
	SumsubTier1LevelName     string
	SumsubTier2LevelName     string
	SumsubTier3LevelName     string
	SumsubTier4LevelName     string
	SumsubWebSDKLinkTTLSeconds int
	GraphWebhookSecret       string
	GraphWebhookPublicBaseURL string
	BybitFallbackEnabled     bool
}

type ApplicationService struct {
	db              *pgxpool.Pool
	pricingEngine   *pricing.PricingEngine
	graph           *graphclient.Client
	primaryExchange exchange.ExchangeClient
	fallbackExchange exchange.ExchangeClient
	encryptor       *appcrypto.PIIEncryptor
	logger          *slog.Logger
	userFSM         *statemachine.UserFSM
	kycOrchestrator *kyc.KYCOrchestrator
	options         Options
}

func NewApplicationService(
	db *pgxpool.Pool,
	pricingEngine *pricing.PricingEngine,
	graph *graphclient.Client,
	primaryExchange exchange.ExchangeClient,
	fallbackExchange exchange.ExchangeClient,
	encryptor *appcrypto.PIIEncryptor,
	logger *slog.Logger,
	options Options,
) *ApplicationService {
	return &ApplicationService{
		db:               db,
		pricingEngine:    pricingEngine,
		graph:            graph,
		primaryExchange:  primaryExchange,
		fallbackExchange: fallbackExchange,
		encryptor:        encryptor,
		logger:           logger,
		userFSM:          statemachine.NewUserFSM(),
		options:          options,
	}
}

func (s *ApplicationService) SetKYCOrchestrator(orchestrator *kyc.KYCOrchestrator) {
	s.kycOrchestrator = orchestrator
}
