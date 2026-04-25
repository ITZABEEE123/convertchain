package service

import (
	"testing"

	"convert-chain/go-engine/internal/domain"
	"convert-chain/go-engine/internal/statemachine"

	"github.com/google/uuid"
)

func TestIsAllowedTradeStatusTransition(t *testing.T) {
	if !isAllowedTradeStatusTransition(string(statemachine.TradeDepositConfirmed), string(statemachine.TradeConversionInProgress)) {
		t.Fatalf("expected deposit_confirmed -> conversion_in_progress to be allowed")
	}
	if isAllowedTradeStatusTransition(string(statemachine.TradePendingDeposit), string(statemachine.TradePayoutCompleted)) {
		t.Fatalf("expected pending_deposit -> payout_completed to be rejected")
	}
}

func TestBuildTradeOperationKeyUsesExplicitKey(t *testing.T) {
	trade := &domain.Trade{ID: uuid.New()}
	key := buildTradeOperationKey(trade, string(statemachine.TradePayoutCompleted), map[string]interface{}{
		"idempotency_key": "  CUSTOM-Key  ",
	})
	if key != "custom-key" {
		t.Fatalf("expected normalized explicit key, got %q", key)
	}
}

func TestResolvePayoutAmountPriority(t *testing.T) {
	actual := int64(4200)
	trade := &domain.Trade{
		ToAmountExpected: 2000,
		ToAmountActual:   &actual,
	}

	if got := resolvePayoutAmount(trade, map[string]interface{}{"to_amount_actual": int64(9900)}); got != 9900 {
		t.Fatalf("expected metadata amount to win, got %d", got)
	}
	if got := resolvePayoutAmount(trade, map[string]interface{}{}); got != 4200 {
		t.Fatalf("expected trade actual amount fallback, got %d", got)
	}
	trade.ToAmountActual = nil
	if got := resolvePayoutAmount(trade, nil); got != 2000 {
		t.Fatalf("expected expected amount fallback, got %d", got)
	}
}
