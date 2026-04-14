// internal/statemachine/trade_fsm_test.go
package statemachine

import (
	"context"
	"testing"
	"time"

	"convert-chain/go-engine/internal/domain"
)

// TestTradeFSM_HappyPath tests the complete successful trade flow.
func TestTradeFSM_HappyPath(t *testing.T) {
	fsm := NewTradeFSM()
	ctx := context.Background()

	trade := &domain.Trade{
		Status:       string(TradeQuoteProvided),
		FromCurrency: "BTC",
		ValidUntil:   time.Now().Add(2 * time.Minute),  // Quote still valid
		ExpiresAt:    time.Now().Add(30 * time.Minute),  // Deposit window open
		Confirmations: 3,                                 // More than enough
	}

	// Walk through the entire happy path
	steps := []struct {
		event    TradeEvent
		expected TradeState
	}{
		{EventQuoteAccepted, TradePendingDeposit},
		{EventDepositDetected, TradeDepositReceived},
		{EventDepositConfirmed, TradeDepositConfirmed},
		{EventConversionStarted, TradeConversionInProgress},
		{EventConversionDone, TradeConversionCompleted},
		{EventPayoutInitiated, TradePayoutPending},
		{EventPayoutSuccess, TradePayoutCompleted},
	}

	for i, step := range steps {
		err := fsm.Transition(ctx, trade, step.event)
		if err != nil {
			t.Fatalf("Step %d (%s): expected success, got: %v", i+1, step.event, err)
		}
		if trade.Status != string(step.expected) {
			t.Fatalf("Step %d: expected state %s, got: %s", i+1, step.expected, trade.Status)
		}
	}
}

// TestTradeFSM_ExpiredQuote tests that accepting an expired quote is blocked.
func TestTradeFSM_ExpiredQuote(t *testing.T) {
	fsm := NewTradeFSM()
	ctx := context.Background()

	trade := &domain.Trade{
		Status:     string(TradeQuoteProvided),
		ValidUntil: time.Now().Add(-5 * time.Minute), // Expired 5 minutes ago
	}

	err := fsm.Transition(ctx, trade, EventQuoteAccepted)
	if err == nil {
		t.Fatal("Expected transition to FAIL — quote is expired")
	}
}

// TestTradeFSM_InsufficientConfirmations tests the BTC confirmation guard.
func TestTradeFSM_InsufficientConfirmations(t *testing.T) {
	fsm := NewTradeFSM()
	ctx := context.Background()

	trade := &domain.Trade{
		Status:        string(TradeDepositReceived),
		FromCurrency:  "BTC",
		Confirmations: 1, // Only 1, need 2
	}

	err := fsm.Transition(ctx, trade, EventDepositConfirmed)
	if err == nil {
		t.Fatal("Expected transition to FAIL — insufficient confirmations")
	}

	// Now give it enough confirmations
	trade.Confirmations = 2
	err = fsm.Transition(ctx, trade, EventDepositConfirmed)
	if err != nil {
		t.Fatalf("Expected transition to succeed with 2 confirmations, got: %v", err)
	}
}

// TestTradeFSM_PayoutRetry tests the retry path after payout failure.
func TestTradeFSM_PayoutRetry(t *testing.T) {
	fsm := NewTradeFSM()
	ctx := context.Background()

	trade := &domain.Trade{
		Status: string(TradePayoutFailed),
	}

	// Retry payout → should go back to PAYOUT_PENDING
	err := fsm.Transition(ctx, trade, EventPayoutInitiated)
	if err != nil {
		t.Fatalf("Expected payout retry to succeed, got: %v", err)
	}
	if trade.Status != string(TradePayoutPending) {
		t.Fatalf("Expected PAYOUT_PENDING after retry, got: %s", trade.Status)
	}
}

// TestTradeFSM_InvalidSkip tests that skipping states is impossible.
func TestTradeFSM_InvalidSkip(t *testing.T) {
	fsm := NewTradeFSM()
	ctx := context.Background()

	trade := &domain.Trade{
		Status: string(TradeQuoteProvided),
	}

	// Try to go directly from QUOTE_PROVIDED to PAYOUT_COMPLETED
	// This should absolutely fail — you can't pay without receiving crypto
	err := fsm.Transition(ctx, trade, EventPayoutSuccess)
	if err == nil {
		t.Fatal("CRITICAL: Transition from QUOTE_PROVIDED to PAYOUT_COMPLETED should be IMPOSSIBLE")
	}
}

// TestTradeFSM_DisputeEscalation tests the dispute path.
func TestTradeFSM_DisputeEscalation(t *testing.T) {
	fsm := NewTradeFSM()
	ctx := context.Background()

	// Payout failed → dispute raised → dispute resolved
	trade := &domain.Trade{
		Status: string(TradePayoutFailed),
	}

	err := fsm.Transition(ctx, trade, EventDisputeRaised)
	if err != nil {
		t.Fatalf("Expected dispute to succeed from PAYOUT_FAILED, got: %v", err)
	}

	err = fsm.Transition(ctx, trade, EventDisputeResolved)
	if err != nil {
		t.Fatalf("Expected dispute resolution to succeed, got: %v", err)
	}
	if trade.Status != string(TradePayoutCompleted) {
		t.Fatalf("Expected PAYOUT_COMPLETED after dispute resolution, got: %s", trade.Status)
	}
}

// TestTradeFSM_CanTransition tests the helper method.
func TestTradeFSM_CanTransition(t *testing.T) {
	fsm := NewTradeFSM()

	trade := &domain.Trade{
		Status:     string(TradeQuoteProvided),
		ValidUntil: time.Now().Add(2 * time.Minute),
	}

	// Should be able to accept the quote
	if !fsm.CanTransition(trade, EventQuoteAccepted) {
		t.Fatal("Expected CanTransition to return true for QUOTE_ACCEPTED")
	}

	// Should NOT be able to confirm deposit (wrong state)
	if fsm.CanTransition(trade, EventDepositConfirmed) {
		t.Fatal("Expected CanTransition to return false for DEPOSIT_CONFIRMED from QUOTE_PROVIDED")
	}
}

// TestTradeFSM_ValidEvents tests the ValidEvents helper.
func TestTradeFSM_ValidEvents(t *testing.T) {
	fsm := NewTradeFSM()

	trade := &domain.Trade{
		Status: string(TradePendingDeposit),
	}

	events := fsm.ValidEvents(trade)
	if len(events) != 2 {
		t.Fatalf("Expected 2 valid events from PENDING_DEPOSIT, got: %d", len(events))
	}
	// Should be DEPOSIT_DETECTED and CANCELLED
}