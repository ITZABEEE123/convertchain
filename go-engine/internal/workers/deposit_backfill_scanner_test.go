package workers

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"convert-chain/go-engine/internal/domain"
	"convert-chain/go-engine/internal/statemachine"
)

func TestDepositBackfillScannerDetectsMissedDeposit(t *testing.T) {
	trade := makeTrade(string(statemachine.TradePendingDeposit), 10*time.Minute)
	repo := newMockTradeRepo(trade)
	blockchain := &mockBlockchain{result: &DepositResult{
		Found:          true,
		AmountReceived: trade.FromAmount,
		Confirmations:  1,
		TxHash:         "backfill_detected_tx",
		Network:        "btc",
		Address:        *trade.DepositAddress,
	}}

	scanner := NewDepositBackfillScanner(repo, blockchain, DefaultDepositPolicySet(), time.Hour, slog.Default())
	scanner.runOnce(context.Background())

	if len(repo.statusUpdates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(repo.statusUpdates))
	}
	if repo.statusUpdates[0].status != string(statemachine.TradeDepositReceived) {
		t.Fatalf("expected deposit received, got %s", repo.statusUpdates[0].status)
	}
}

func TestDepositBackfillScannerConfirmsMissedFinality(t *testing.T) {
	trade := makeTrade(string(statemachine.TradeDepositReceived), 10*time.Minute)
	repo := newMockTradeRepo(trade)
	blockchain := &mockBlockchain{result: &DepositResult{
		Found:          true,
		AmountReceived: trade.FromAmount,
		Confirmations:  2,
		TxHash:         "backfill_confirmed_tx",
		Network:        "btc",
		Address:        *trade.DepositAddress,
	}}

	scanner := NewDepositBackfillScanner(repo, blockchain, DefaultDepositPolicySet(), time.Hour, slog.Default())
	scanner.runOnce(context.Background())

	if len(repo.statusUpdates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(repo.statusUpdates))
	}
	if repo.statusUpdates[0].status != string(statemachine.TradeDepositConfirmed) {
		t.Fatalf("expected deposit confirmed, got %s", repo.statusUpdates[0].status)
	}
}

func TestDepositBackfillScannerWrongAmountMovesToDispute(t *testing.T) {
	trade := makeTrade(string(statemachine.TradePendingDeposit), 10*time.Minute)
	repo := newMockTradeRepo(trade)
	blockchain := &mockBlockchain{result: &DepositResult{
		Found:          true,
		AmountReceived: trade.FromAmount + 1,
		Confirmations:  2,
		TxHash:         "backfill_wrong_amount_tx",
		Network:        "btc",
		Address:        *trade.DepositAddress,
	}}

	scanner := NewDepositBackfillScanner(repo, blockchain, DefaultDepositPolicySet(), time.Hour, slog.Default())
	scanner.runOnce(context.Background())

	if len(repo.statusUpdates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(repo.statusUpdates))
	}
	if repo.statusUpdates[0].status != string(statemachine.TradeDispute) {
		t.Fatalf("expected dispute, got %s", repo.statusUpdates[0].status)
	}
	if got := repo.statusUpdates[0].metadata["reason"]; got != "backfill_wrong_deposit_amount" {
		t.Fatalf("expected backfill wrong amount reason, got %#v", got)
	}
}

func TestDepositBackfillScannerFinalityDropMovesToDispute(t *testing.T) {
	trade := makeTrade(string(statemachine.TradeConversionCompleted), 10*time.Minute)
	repo := newMockTradeRepo(trade)
	blockchain := &mockBlockchain{result: &DepositResult{
		Found:          true,
		AmountReceived: trade.FromAmount,
		Confirmations:  1,
		TxHash:         "backfill_finality_drop_tx",
		Network:        "btc",
		Address:        *trade.DepositAddress,
	}}

	scanner := NewDepositBackfillScanner(repo, blockchain, DefaultDepositPolicySet(), time.Hour, slog.Default())
	scanner.runOnce(context.Background())

	if len(repo.statusUpdates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(repo.statusUpdates))
	}
	if repo.statusUpdates[0].status != string(statemachine.TradeDispute) {
		t.Fatalf("expected dispute for finality drop, got %s", repo.statusUpdates[0].status)
	}
}

func TestDepositBackfillScannerDuplicateTxHashMovesToDispute(t *testing.T) {
	txHash := "backfill_duplicate_tx"
	existing := makeTrade(string(statemachine.TradeDepositConfirmed), 10*time.Minute)
	existing.DepositTxHash = &txHash
	trade := makeTrade(string(statemachine.TradePendingDeposit), 10*time.Minute)
	repo := newMockTradeRepo(existing, trade)
	blockchain := &mockBlockchain{result: &DepositResult{
		Found:          true,
		AmountReceived: trade.FromAmount,
		Confirmations:  2,
		TxHash:         txHash,
		Network:        "btc",
		Address:        *trade.DepositAddress,
	}}

	scanner := NewDepositBackfillScanner(repo, blockchain, DefaultDepositPolicySet(), time.Hour, slog.Default())
	scanner.runOnce(context.Background())

	if len(repo.statusUpdates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(repo.statusUpdates))
	}
	if repo.statusUpdates[0].status != string(statemachine.TradeDispute) {
		t.Fatalf("expected dispute for duplicate tx hash, got %s", repo.statusUpdates[0].status)
	}
}

func TestDepositBackfillScannerSkipsUnsupportedTradeWithoutAddress(t *testing.T) {
	trade := &domain.Trade{Status: string(statemachine.TradePendingDeposit)}
	repo := newMockTradeRepo(trade)
	blockchain := &mockBlockchain{result: &DepositResult{Found: true}}

	scanner := NewDepositBackfillScanner(repo, blockchain, DefaultDepositPolicySet(), time.Hour, slog.Default())
	scanner.runOnce(context.Background())

	if len(repo.statusUpdates) != 0 {
		t.Fatalf("expected no status updates for missing address, got %d", len(repo.statusUpdates))
	}
}
