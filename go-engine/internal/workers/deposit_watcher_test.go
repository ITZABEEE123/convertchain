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

// mockTradeRepo is a fake TradeRepository that stores trades in memory.
type mockTradeRepo struct {
	trades        map[string]*domain.Trade
	statusUpdates []statusUpdate
}

type statusUpdate struct {
	tradeID  string
	status   string
	metadata map[string]interface{}
}

func newMockTradeRepo(trades ...*domain.Trade) *mockTradeRepo {
	m := &mockTradeRepo{trades: make(map[string]*domain.Trade)}
	for _, trade := range trades {
		m.trades[trade.ID.String()] = trade
	}
	return m
}

func (m *mockTradeRepo) GetTradesByStatus(_ context.Context, status string) ([]*domain.Trade, error) {
	var result []*domain.Trade
	for _, trade := range m.trades {
		if trade.Status == status {
			result = append(result, trade)
		}
	}
	return result, nil
}

func (m *mockTradeRepo) GetTradeByID(_ context.Context, tradeID string) (*domain.Trade, error) {
	trade, ok := m.trades[tradeID]
	if !ok {
		return nil, errors.New("trade not found")
	}
	return trade, nil
}

func (m *mockTradeRepo) UpdateTradeStatus(_ context.Context, tradeID string, status string, metadata map[string]interface{}) error {
	m.statusUpdates = append(m.statusUpdates, statusUpdate{tradeID: tradeID, status: status, metadata: metadata})
	if trade, ok := m.trades[tradeID]; ok {
		trade.Status = status
	}
	return nil
}

func (m *mockTradeRepo) GetPendingPayouts(_ context.Context) ([]*domain.Trade, error) {
	return nil, nil
}

func (m *mockTradeRepo) MarkPayoutComplete(_ context.Context, _ string, _ string) error {
	return nil
}

// mockBlockchain is a fake BlockchainClient.
type mockBlockchain struct {
	result *DepositResult
	err    error
}

func (m *mockBlockchain) CheckDeposit(_ context.Context, _ string, _ string, _ int64) (*DepositResult, error) {
	return m.result, m.err
}

func makeTrade(status string, expiresIn time.Duration) *domain.Trade {
	address := "0xDeadBeef"
	return &domain.Trade{
		ID:             uuid.New(),
		UserID:         uuid.New(),
		Status:         status,
		FromCurrency:   "BTC",
		FromAmount:     100000000,
		DepositAddress: &address,
		ExpiresAt:      time.Now().Add(expiresIn),
	}
}

func TestDepositWatcher_NoDeposit(t *testing.T) {
	trade := makeTrade(string(statemachine.TradePendingDeposit), 10*time.Minute)
	repo := newMockTradeRepo(trade)
	blockchain := &mockBlockchain{result: &DepositResult{Found: false}}

	watcher := NewDepositWatcher(repo, blockchain, statemachine.NewTradeFSM(), time.Hour, slog.Default())
	watcher.runOnce(context.Background())

	if len(repo.statusUpdates) != 0 {
		t.Errorf("expected 0 status updates, got %d", len(repo.statusUpdates))
	}
}

func TestDepositWatcher_DepositDetected(t *testing.T) {
	trade := makeTrade(string(statemachine.TradePendingDeposit), 10*time.Minute)
	repo := newMockTradeRepo(trade)
	blockchain := &mockBlockchain{
		result: &DepositResult{Found: true, AmountReceived: 100000000, Confirmations: 1, TxHash: "0xabc"},
	}

	watcher := NewDepositWatcher(repo, blockchain, statemachine.NewTradeFSM(), time.Hour, slog.Default())
	watcher.runOnce(context.Background())

	if len(repo.statusUpdates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(repo.statusUpdates))
	}
	if repo.statusUpdates[0].status != string(statemachine.TradeDepositReceived) {
		t.Errorf("expected %s, got %s", statemachine.TradeDepositReceived, repo.statusUpdates[0].status)
	}
}

func TestDepositWatcher_DepositConfirmed(t *testing.T) {
	trade := makeTrade(string(statemachine.TradeDepositReceived), 10*time.Minute)
	repo := newMockTradeRepo(trade)
	blockchain := &mockBlockchain{
		result: &DepositResult{Found: true, AmountReceived: 100000000, Confirmations: 3, TxHash: "0xdef"},
	}

	watcher := NewDepositWatcher(repo, blockchain, statemachine.NewTradeFSM(), time.Hour, slog.Default())
	watcher.runOnce(context.Background())

	if len(repo.statusUpdates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(repo.statusUpdates))
	}
	if repo.statusUpdates[0].status != string(statemachine.TradeDepositConfirmed) {
		t.Errorf("expected %s, got %s", statemachine.TradeDepositConfirmed, repo.statusUpdates[0].status)
	}
}

func TestDepositWatcher_ExpiredTrade(t *testing.T) {
	trade := makeTrade(string(statemachine.TradePendingDeposit), -5*time.Minute)
	repo := newMockTradeRepo(trade)
	blockchain := &mockBlockchain{result: &DepositResult{Found: false}}

	watcher := NewDepositWatcher(repo, blockchain, statemachine.NewTradeFSM(), time.Hour, slog.Default())
	watcher.runOnce(context.Background())

	if len(repo.statusUpdates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(repo.statusUpdates))
	}
	if repo.statusUpdates[0].status != string(statemachine.TradeCancelled) {
		t.Errorf("expected %s, got %s", statemachine.TradeCancelled, repo.statusUpdates[0].status)
	}
}

func TestDepositWatcher_BlockchainError(t *testing.T) {
	trade := makeTrade(string(statemachine.TradePendingDeposit), 10*time.Minute)
	repo := newMockTradeRepo(trade)
	blockchain := &mockBlockchain{err: errors.New("connection timeout")}

	watcher := NewDepositWatcher(repo, blockchain, statemachine.NewTradeFSM(), time.Hour, slog.Default())
	watcher.runOnce(context.Background())

	if len(repo.statusUpdates) != 0 {
		t.Errorf("expected 0 status updates on error, got %d", len(repo.statusUpdates))
	}
}