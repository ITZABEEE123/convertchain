package service

import (
	"log/slog"

	appcrypto "convert-chain/go-engine/internal/crypto"
	graphclient "convert-chain/go-engine/internal/graph"
	"convert-chain/go-engine/internal/kyc"
	"convert-chain/go-engine/internal/pricing"
	"convert-chain/go-engine/internal/statemachine"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Options struct {
	AutoApproveKYC      bool
	SmileIDPartnerID    string
	SmileIDAPIKey       string
	SumsubWebhookSecret string
}

type ApplicationService struct {
	db              *pgxpool.Pool
	pricingEngine   *pricing.PricingEngine
	graph           *graphclient.Client
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
	encryptor *appcrypto.PIIEncryptor,
	logger *slog.Logger,
	options Options,
) *ApplicationService {
	return &ApplicationService{
		db:            db,
		pricingEngine: pricingEngine,
		graph:         graph,
		encryptor:     encryptor,
		logger:        logger,
		userFSM:       statemachine.NewUserFSM(),
		options:       options,
	}
}

func (s *ApplicationService) SetKYCOrchestrator(orchestrator *kyc.KYCOrchestrator) {
	s.kycOrchestrator = orchestrator
}
