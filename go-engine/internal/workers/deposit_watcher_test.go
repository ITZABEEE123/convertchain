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

func (m *mockTradeRepo) GetTradeByDepositTxHash(_ context.Context, txHash string) (*domain.Trade, error) {
	for _, trade := range m.trades {
		if trade.DepositTxHash != nil && *trade.DepositTxHash == txHash {
			return trade, nil
		}
	}
	return nil, nil
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
	if _, ok := repo.statusUpdates[0].metadata["idempotency_key"]; !ok {
		t.Fatalf("expected idempotency_key metadata for deposit detection")
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
	if _, ok := repo.statusUpdates[0].metadata["idempotency_key"]; !ok {
		t.Fatalf("expected idempotency_key metadata for deposit confirmation")
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
	if _, ok := repo.statusUpdates[0].metadata["idempotency_key"]; !ok {
		t.Fatalf("expected idempotency_key metadata for expiration cancellation")
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

func TestDepositWatcher_RespectsDetectionConfirmationPolicy(t *testing.T) {
	trade := makeTrade(string(statemachine.TradePendingDeposit), 10*time.Minute)
	repo := newMockTradeRepo(trade)
	blockchain := &mockBlockchain{
		result: &DepositResult{
			Found:          true,
			AmountReceived: trade.FromAmount,
			Confirmations:  1,
			TxHash:         "btc_low_conf",
			Network:        "btc",
			Address:        *trade.DepositAddress,
		},
	}
	policies := DefaultDepositPolicySet()
	policies.Put(DepositConfirmationPolicy{
		Currency:               "BTC",
		Network:                "btc",
		DetectionConfirmations: 2,
		FinalityConfirmations:  3,
	})

	watcher := NewDepositWatcherWithPolicy(repo, blockchain, statemachine.NewTradeFSM(), policies, time.Hour, slog.Default())
	watcher.runOnce(context.Background())

	if len(repo.statusUpdates) != 0 {
		t.Fatalf("expected no status update below detection threshold, got %d", len(repo.statusUpdates))
	}
}

func TestDepositWatcher_DuplicateTxHashMovesToDispute(t *testing.T) {
	existing := makeTrade(string(statemachine.TradeDepositConfirmed), 10*time.Minute)
	txHash := "duplicate_tx_hash"
	existing.DepositTxHash = &txHash
	trade := makeTrade(string(statemachine.TradePendingDeposit), 10*time.Minute)
	repo := newMockTradeRepo(existing, trade)
	blockchain := &mockBlockchain{
		result: &DepositResult{
			Found:          true,
			AmountReceived: trade.FromAmount,
			Confirmations:  2,
			TxHash:         txHash,
			Network:        "btc",
			Address:        *trade.DepositAddress,
		},
	}

	watcher := NewDepositWatcher(repo, blockchain, statemachine.NewTradeFSM(), time.Hour, slog.Default())
	watcher.runOnce(context.Background())

	if len(repo.statusUpdates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(repo.statusUpdates))
	}
	if repo.statusUpdates[0].status != string(statemachine.TradeDispute) {
		t.Fatalf("expected dispute for duplicate tx hash, got %s", repo.statusUpdates[0].status)
	}
	if got := repo.statusUpdates[0].metadata["reason"]; got != "duplicate_deposit_tx_hash" {
		t.Fatalf("expected duplicate_deposit_tx_hash reason, got %#v", got)
	}
}

func TestDepositWatcher_WrongAmountMovesToDispute(t *testing.T) {
	trade := makeTrade(string(statemachine.TradePendingDeposit), 10*time.Minute)
	repo := newMockTradeRepo(trade)
	blockchain := &mockBlockchain{
		result: &DepositResult{
			Found:          true,
			AmountReceived: trade.FromAmount - 1,
			Confirmations:  2,
			TxHash:         "wrong_amount_tx",
			Network:        "btc",
			Address:        *trade.DepositAddress,
		},
	}

	watcher := NewDepositWatcher(repo, blockchain, statemachine.NewTradeFSM(), time.Hour, slog.Default())
	watcher.runOnce(context.Background())

	if len(repo.statusUpdates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(repo.statusUpdates))
	}
	if repo.statusUpdates[0].status != string(statemachine.TradeDispute) {
		t.Fatalf("expected dispute for wrong amount, got %s", repo.statusUpdates[0].status)
	}
	if got := repo.statusUpdates[0].metadata["reason"]; got != "wrong_deposit_amount" {
		t.Fatalf("expected wrong_deposit_amount reason, got %#v", got)
	}
}

func TestDepositWatcher_WrongNetworkMovesToDispute(t *testing.T) {
	trade := makeTrade(string(statemachine.TradePendingDeposit), 10*time.Minute)
	trade.FromCurrency = "USDC"
	address := "ethereum:0xDeadBeef"
	trade.DepositAddress = &address
	repo := newMockTradeRepo(trade)
	blockchain := &mockBlockchain{
		result: &DepositResult{
			Found:          true,
			AmountReceived: trade.FromAmount,
			Confirmations:  12,
			TxHash:         "wrong_network_tx",
			Network:        "polygon",
			Address:        "0xDeadBeef",
		},
	}

	watcher := NewDepositWatcher(repo, blockchain, statemachine.NewTradeFSM(), time.Hour, slog.Default())
	watcher.runOnce(context.Background())

	if len(repo.statusUpdates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(repo.statusUpdates))
	}
	if repo.statusUpdates[0].status != string(statemachine.TradeDispute) {
		t.Fatalf("expected dispute for wrong network, got %s", repo.statusUpdates[0].status)
	}
	if got := repo.statusUpdates[0].metadata["reason"]; got != "wrong_deposit_network" {
		t.Fatalf("expected wrong_deposit_network reason, got %#v", got)
	}
}

func TestDepositWatcher_ReorgOrReplacementMovesToDispute(t *testing.T) {
	trade := makeTrade(string(statemachine.TradeDepositReceived), 10*time.Minute)
	repo := newMockTradeRepo(trade)
	blockchain := &mockBlockchain{
		result: &DepositResult{
			Found:          true,
			AmountReceived: trade.FromAmount,
			Confirmations:  2,
			TxHash:         "reorg_tx",
			Network:        "btc",
			Address:        *trade.DepositAddress,
			Reversed:       true,
		},
	}

	watcher := NewDepositWatcher(repo, blockchain, statemachine.NewTradeFSM(), time.Hour, slog.Default())
	watcher.runOnce(context.Background())

	if len(repo.statusUpdates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(repo.statusUpdates))
	}
	if repo.statusUpdates[0].status != string(statemachine.TradeDispute) {
		t.Fatalf("expected dispute for reorg/replacement risk, got %s", repo.statusUpdates[0].status)
	}
	if got := repo.statusUpdates[0].metadata["reason"]; got != "deposit_reorg_or_replacement_risk" {
		t.Fatalf("expected deposit_reorg_or_replacement_risk reason, got %#v", got)
	}
}

func TestDepositWatcher_HighRiskWalletMovesToDispute(t *testing.T) {
	t.Setenv("HIGH_RISK_WALLET_BLOCKLIST", "bc1-risk-wallet")

	trade := makeTrade(string(statemachine.TradeDepositReceived), 10*time.Minute)
	address := "bc1-risk-wallet"
	trade.DepositAddress = &address
	repo := newMockTradeRepo(trade)
	blockchain := &mockBlockchain{
		result: &DepositResult{
			Found:          true,
			AmountReceived: trade.FromAmount,
			Confirmations:  2,
			TxHash:         "risk_tx",
			Network:        "btc",
			Address:        "bc1-risk-wallet",
		},
	}

	watcher := NewDepositWatcher(repo, blockchain, statemachine.NewTradeFSM(), time.Hour, slog.Default())
	watcher.runOnce(context.Background())

	if len(repo.statusUpdates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(repo.statusUpdates))
	}
	if repo.statusUpdates[0].status != string(statemachine.TradeDispute) {
		t.Fatalf("expected dispute status, got %s", repo.statusUpdates[0].status)
	}
	if got := repo.statusUpdates[0].metadata["reason"]; got != "high_risk_wallet_blocklist" {
		t.Fatalf("expected high_risk_wallet_blocklist reason, got %#v", got)
	}
}
