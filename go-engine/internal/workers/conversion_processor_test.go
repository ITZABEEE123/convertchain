package workers

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"convert-chain/go-engine/internal/domain"
	"convert-chain/go-engine/internal/statemachine"

	"github.com/google/uuid"
)

type conversionRepoStub struct {
	readyTrades      []*domain.Trade
	inProgressTrades []*domain.Trade
	updates          []statusUpdateCall
}

func (r *conversionRepoStub) GetTradesByStatus(_ context.Context, status string) ([]*domain.Trade, error) {
	switch status {
	case string(statemachine.TradeDepositConfirmed):
		return r.readyTrades, nil
	case string(statemachine.TradeConversionInProgress):
		return r.inProgressTrades, nil
	default:
		return nil, nil
	}
}

func (r *conversionRepoStub) GetTradeByID(_ context.Context, _ string) (*domain.Trade, error) {
	return nil, nil
}

func (r *conversionRepoStub) UpdateTradeStatus(_ context.Context, tradeID string, status string, metadata map[string]interface{}) error {
	r.updates = append(r.updates, statusUpdateCall{tradeID: tradeID, status: status, metadata: metadata})
	return nil
}

func (r *conversionRepoStub) GetPendingPayouts(_ context.Context) ([]*domain.Trade, error) {
	return nil, nil
}

func (r *conversionRepoStub) MarkPayoutComplete(_ context.Context, _ string, _ string) error {
	return nil
}

type conversionExecutorStub struct {
	result *ConversionResult
	err    error
}

func (c *conversionExecutorStub) ConvertToStable(_ context.Context, _ string, _ int64) (*ConversionResult, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.result, nil
}

func TestConversionProcessorExecutesConfirmedTrade(t *testing.T) {
	trade := &domain.Trade{
		ID:           uuid.New(),
		UserID:       uuid.New(),
		Status:       string(statemachine.TradeDepositConfirmed),
		FromCurrency: "BTC",
		FromAmount:   25000000,
	}

	repo := &conversionRepoStub{readyTrades: []*domain.Trade{trade}}
	executor := &conversionExecutorStub{result: &ConversionResult{
		Exchange:    "binance",
		OrderID:     "order-123",
		Symbol:      "BTCUSDT",
		Status:      "FILLED",
		ExecutedQty: "0.25",
		QuoteQty:    "15000",
	}}

	processor := NewConversionProcessor(repo, executor, 0, slog.Default())
	processor.runOnce(context.Background())

	if len(repo.updates) != 2 {
		t.Fatalf("expected 2 status updates, got %d", len(repo.updates))
	}
	if repo.updates[0].status != string(statemachine.TradeConversionInProgress) {
		t.Fatalf("expected first status %s, got %s", statemachine.TradeConversionInProgress, repo.updates[0].status)
	}
	if repo.updates[1].status != string(statemachine.TradeConversionCompleted) {
		t.Fatalf("expected second status %s, got %s", statemachine.TradeConversionCompleted, repo.updates[1].status)
	}
	if got := repo.updates[1].metadata["exchange_order_id"]; got != "order-123" {
		t.Fatalf("expected exchange_order_id order-123, got %#v", got)
	}
}

func TestConversionProcessorLeavesTradeReadyOnExecutionFailure(t *testing.T) {
	trade := &domain.Trade{
		ID:           uuid.New(),
		UserID:       uuid.New(),
		Status:       string(statemachine.TradeDepositConfirmed),
		FromCurrency: "BTC",
		FromAmount:   100000000,
	}

	repo := &conversionRepoStub{readyTrades: []*domain.Trade{trade}}
	executor := &conversionExecutorStub{err: errors.New("exchange unavailable")}

	processor := NewConversionProcessor(repo, executor, 0, slog.Default())
	processor.runOnce(context.Background())

	if len(repo.updates) != 0 {
		t.Fatalf("expected 0 status updates, got %d", len(repo.updates))
	}
}

func TestConversionProcessorRecoversInProgressTrade(t *testing.T) {
	orderID := "order-recover-456"
	trade := &domain.Trade{
		ID:              uuid.New(),
		UserID:          uuid.New(),
		Status:          string(statemachine.TradeConversionInProgress),
		ExchangeOrderID: &orderID,
		UpdatedAt:       time.Now(),
	}

	repo := &conversionRepoStub{inProgressTrades: []*domain.Trade{trade}}
	executor := &conversionExecutorStub{}

	processor := NewConversionProcessor(repo, executor, 0, slog.Default())
	processor.recoverInProgressTrades(context.Background())

	if len(repo.updates) != 0 {
		t.Fatalf("expected no update for a fresh in-progress conversion, got %d", len(repo.updates))
	}
}

func TestConversionProcessorEscalatesStaleInProgressTrade(t *testing.T) {
	orderID := "order-recover-456"
	trade := &domain.Trade{
		ID:              uuid.New(),
		UserID:          uuid.New(),
		Status:          string(statemachine.TradeConversionInProgress),
		ExchangeOrderID: &orderID,
		UpdatedAt:       time.Now().Add(-conversionReconciliationTimeout - time.Minute),
	}

	repo := &conversionRepoStub{inProgressTrades: []*domain.Trade{trade}}
	executor := &conversionExecutorStub{}

	processor := NewConversionProcessor(repo, executor, 0, slog.Default())
	processor.recoverInProgressTrades(context.Background())

	if len(repo.updates) != 1 {
		t.Fatalf("expected 1 recovery update, got %d", len(repo.updates))
	}
	if repo.updates[0].status != string(statemachine.TradeDispute) {
		t.Fatalf("expected recovered status %s, got %s", statemachine.TradeDispute, repo.updates[0].status)
	}
}
