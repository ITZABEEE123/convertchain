package workers

import (
	"context"
	"log/slog"
	"testing"

	"convert-chain/go-engine/internal/domain"
	"convert-chain/go-engine/internal/statemachine"

	"github.com/google/uuid"
)

type payoutRepoStub struct {
	readyTrades   []*domain.Trade
	pendingTrades []*domain.Trade
	updates       []statusUpdateCall
	completions   []completionCall
}

type statusUpdateCall struct {
	tradeID  string
	status   string
	metadata map[string]interface{}
}

type completionCall struct {
	tradeID   string
	payoutRef string
}

func (p *payoutRepoStub) GetTradesByStatus(_ context.Context, status string) ([]*domain.Trade, error) {
	switch status {
	case string(statemachine.TradePayoutPending):
		return p.pendingTrades, nil
	default:
		return nil, nil
	}
}

func (p *payoutRepoStub) GetTradeByID(_ context.Context, _ string) (*domain.Trade, error) {
	return nil, nil
}

func (p *payoutRepoStub) GetTradeByDepositTxHash(_ context.Context, _ string) (*domain.Trade, error) {
	return nil, nil
}

func (p *payoutRepoStub) UpdateTradeStatus(_ context.Context, tradeID string, status string, metadata map[string]interface{}) error {
	p.updates = append(p.updates, statusUpdateCall{tradeID: tradeID, status: status, metadata: metadata})
	return nil
}

func (p *payoutRepoStub) GetPendingPayouts(_ context.Context) ([]*domain.Trade, error) {
	return p.readyTrades, nil
}

func (p *payoutRepoStub) MarkPayoutComplete(_ context.Context, tradeID string, payoutRef string) error {
	p.completions = append(p.completions, completionCall{tradeID: tradeID, payoutRef: payoutRef})
	return nil
}

type payoutGraphStub struct {
	payoutRef string
	status    string
}

func (p *payoutGraphStub) ConvertAndPay(_ context.Context, _ string, _ int64) (string, error) {
	return p.payoutRef, nil
}

func (p *payoutGraphStub) GetPayoutStatus(_ context.Context, _ string) (string, error) {
	return p.status, nil
}

func TestPayoutProcessorInitiatesPayoutAndMarksPending(t *testing.T) {
	bankID := uuid.New()
	repo := &payoutRepoStub{
		readyTrades: []*domain.Trade{{
			ID:               uuid.New(),
			UserID:           uuid.New(),
			BankAccID:        &bankID,
			Status:           string(statemachine.TradeConversionCompleted),
			ToAmountExpected: 125000,
		}},
	}
	graph := &payoutGraphStub{payoutRef: "payout_123", status: "pending"}
	processor := NewPayoutProcessor(repo, graph, 0, slog.Default())

	processor.runOnce(context.Background())

	if len(repo.updates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(repo.updates))
	}
	if repo.updates[0].status != string(statemachine.TradePayoutPending) {
		t.Fatalf("expected %s, got %s", statemachine.TradePayoutPending, repo.updates[0].status)
	}
	if got := repo.updates[0].metadata["payout_ref"]; got != "payout_123" {
		t.Fatalf("expected payout_ref payout_123, got %#v", got)
	}
	if _, ok := repo.updates[0].metadata["idempotency_key"]; !ok {
		t.Fatalf("expected idempotency_key on payout pending update")
	}
}

func TestPayoutProcessorCompletesPendingPayoutFromProviderStatus(t *testing.T) {
	payoutRef := "payout_456"
	repo := &payoutRepoStub{
		pendingTrades: []*domain.Trade{{
			ID:            uuid.New(),
			UserID:        uuid.New(),
			Status:        string(statemachine.TradePayoutPending),
			GraphPayoutID: &payoutRef,
		}},
	}
	graph := &payoutGraphStub{status: "completed"}
	processor := NewPayoutProcessor(repo, graph, 0, slog.Default())

	processor.runOnce(context.Background())

	if len(repo.completions) != 1 {
		t.Fatalf("expected 1 payout completion, got %d", len(repo.completions))
	}
	if repo.completions[0].payoutRef != payoutRef {
		t.Fatalf("expected payout ref %s, got %s", payoutRef, repo.completions[0].payoutRef)
	}
}
