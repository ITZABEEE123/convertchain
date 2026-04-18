package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"convert-chain/go-engine/internal/domain"
	graphclient "convert-chain/go-engine/internal/graph"
	"convert-chain/go-engine/internal/workers"
)

type SandboxBlockchainClient struct {
	mu       sync.Mutex
	checksBy map[string]int
}

func NewSandboxBlockchainClient() *SandboxBlockchainClient {
	return &SandboxBlockchainClient{checksBy: make(map[string]int)}
}

func (s *SandboxBlockchainClient) CheckDeposit(_ context.Context, currency string, address string, expectedAmount int64) (*workers.DepositResult, error) {
	if !strings.HasPrefix(address, "sandbox://") {
		return &workers.DepositResult{Found: false}, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.checksBy[address]++
	checks := s.checksBy[address]
	required := sandboxRequiredConfirmations(currency)

	switch checks {
	case 1:
		return &workers.DepositResult{Found: false}, nil
	case 2:
		return &workers.DepositResult{
			Found:          true,
			AmountReceived: expectedAmount,
			Confirmations:  1,
			TxHash:         fmt.Sprintf("sandbox_tx_%s_%d", strings.ToLower(currency), checks),
		}, nil
	default:
		return &workers.DepositResult{
			Found:          true,
			AmountReceived: expectedAmount,
			Confirmations:  required,
			TxHash:         fmt.Sprintf("sandbox_tx_%s_%d", strings.ToLower(currency), checks),
		}, nil
	}
}

type GraphSandboxPayoutClient struct {
	app    *ApplicationService
	graph  *graphclient.Client
	logger *slog.Logger
}

func NewGraphSandboxPayoutClient(app *ApplicationService, graph *graphclient.Client, logger *slog.Logger) *GraphSandboxPayoutClient {
	return &GraphSandboxPayoutClient{app: app, graph: graph, logger: logger}
}

func (g *GraphSandboxPayoutClient) ConvertAndPay(ctx context.Context, bankAccountID string, payoutAmount int64) (string, error) {
	if g.graph == nil {
		return "", fmt.Errorf("graph client is not configured")
	}

	bankAccount, err := g.app.getBankAccountByID(ctx, bankAccountID)
	if err != nil {
		return "", fmt.Errorf("load bank account: %w", err)
	}

	walletAccount, err := g.graph.GetWalletAccountByCurrency(ctx, "NGN")
	if err != nil {
		return "", fmt.Errorf("load NGN wallet account: %w", err)
	}

	destinationID, err := g.ensurePayoutDestination(ctx, bankAccount, walletAccount.ID)
	if err != nil {
		return "", err
	}

	if g.graph.IsSandbox() {
		if err := g.seedSandboxFunds(ctx, payoutAmount); err != nil {
			return "", err
		}
	}

	reference := fmt.Sprintf("sandbox-payout-%d", time.Now().UnixNano())
	payout, err := g.graph.CreatePayout(ctx, graphclient.CreatePayoutRequest{
		AccountID:     walletAccount.ID,
		SourceType:    "wallet_account",
		DestinationID: destinationID,
		Currency:      "NGN",
		Amount:        payoutAmount,
		Reference:     reference,
		Remarks:       fmt.Sprintf("ConvertChain payout %s", bankAccountID),
	})
	if err != nil {
		return "", fmt.Errorf("create payout: %w", err)
	}

	payoutID := payout.ID
	if payoutID == "" {
		return "", fmt.Errorf("payout response did not include an id")
	}

	deadline := time.Now().Add(20 * time.Second)
	current := payout
	for {
		status := strings.ToLower(strings.TrimSpace(current.Status))
		switch status {
		case "successful", "success", "completed", "paid":
			return payoutID, nil
		case "failed", "cancelled", "reversed", "rejected":
			return "", fmt.Errorf("payout failed with status %s", status)
		}

		if time.Now().After(deadline) {
			if g.graph.IsSandbox() {
				g.logger.Info("accepting queued sandbox payout as complete for local validation", "payout_id", payoutID, "status", status)
				return payoutID, nil
			}
			return "", fmt.Errorf("timed out waiting for payout %s to complete (last status: %s)", payoutID, status)
		}

		time.Sleep(2 * time.Second)
		current, err = g.graph.FetchPayout(ctx, payoutID)
		if err != nil {
			return "", fmt.Errorf("poll payout status: %w", err)
		}
	}
}

func (g *GraphSandboxPayoutClient) ensurePayoutDestination(ctx context.Context, bankAccount *domain.BankAccount, walletAccountID string) (string, error) {
	if bankAccount.GraphDestID != nil && strings.TrimSpace(*bankAccount.GraphDestID) != "" {
		return strings.TrimSpace(*bankAccount.GraphDestID), nil
	}

	destination, err := g.graph.CreatePayoutDestination(ctx, graphclient.CreatePayoutDestinationRequest{
		AccountID:     walletAccountID,
		SourceType:    "wallet_account",
		Label:         fmt.Sprintf("convertchain-%s-%s", bankAccount.UserID.String()[:8], bankAccount.AccountNumber),
		Type:          "nip",
		AccountType:   "personal",
		BankCode:      bankAccount.BankCode,
		AccountNumber: bankAccount.AccountNumber,
	})
	if err != nil {
		return "", fmt.Errorf("create payout destination: %w", err)
	}

	if destination.AccountName != "" || destination.BankName != "" {
		_, err = g.app.db.Exec(ctx, `
            UPDATE bank_accounts
            SET graph_dest_id = $2,
                account_name = COALESCE(NULLIF($3, ''), account_name),
                bank_name = COALESCE(NULLIF($4, ''), bank_name)
            WHERE id = $1::uuid
        `, bankAccount.ID.String(), destination.ID, destination.AccountName, destination.BankName)
	} else {
		_, err = g.app.db.Exec(ctx, `
            UPDATE bank_accounts
            SET graph_dest_id = $2
            WHERE id = $1::uuid
        `, bankAccount.ID.String(), destination.ID)
	}
	if err != nil {
		return "", fmt.Errorf("persist payout destination: %w", err)
	}

	return destination.ID, nil
}

func (g *GraphSandboxPayoutClient) seedSandboxFunds(ctx context.Context, payoutAmount int64) error {
	fundingAccount, err := g.graph.GetFundingBankAccountByCurrency(ctx, "NGN")
	if err != nil {
		return fmt.Errorf("load sandbox funding bank account: %w", err)
	}

	topUpAmount := payoutAmount
	if topUpAmount < 10000 {
		topUpAmount = 10000
	}

	_, err = g.graph.MockDeposit(ctx, graphclient.MockDepositRequest{
		AccountID:  fundingAccount.ID,
		SourceType: "bank_account",
		Currency:   "NGN",
		Amount:     topUpAmount,
		Reference:  fmt.Sprintf("sandbox-funding-%d", time.Now().UnixNano()),
	})
	if err != nil {
		return fmt.Errorf("seed sandbox funds: %w", err)
	}

	g.logger.Info("seeded graph sandbox funds", "amount", topUpAmount, "funding_account_id", fundingAccount.ID)
	return nil
}

func sandboxRequiredConfirmations(currency string) int {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "ETH", "USDC":
		return 12
	case "BTC", "USDT", "BNB":
		return 2
	default:
		return 2
	}
}
