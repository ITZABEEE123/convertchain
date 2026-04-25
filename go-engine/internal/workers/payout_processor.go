package workers

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"convert-chain/go-engine/internal/statemachine"
)

// PayoutProcessor runs every 30 seconds and advances trades through the payout
// stages. It initiates new payouts for DEPOSIT_CONFIRMED trades and reconciles
// PAYOUT_PENDING trades using Graph status polling while webhooks remain the
// primary completion signal.
type PayoutProcessor struct {
	trades   TradeRepository
	graph    GraphFinanceClient
	interval time.Duration
	logger   *slog.Logger
}

// NewPayoutProcessor constructs a PayoutProcessor.
func NewPayoutProcessor(
	trades TradeRepository,
	graph GraphFinanceClient,
	interval time.Duration,
	logger *slog.Logger,
) *PayoutProcessor {
	return &PayoutProcessor{
		trades:   trades,
		graph:    graph,
		interval: interval,
		logger:   logger,
	}
}

// Run starts the payout processing loop. Call this in a goroutine.
func (p *PayoutProcessor) Run(ctx context.Context) {
	p.logger.Info("payout processor starting", "interval", p.interval)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	p.runOnce(ctx)

	for {
		select {
		case <-ticker.C:
			p.runOnce(ctx)
		case <-ctx.Done():
			p.logger.Info("payout processor shutting down")
			return
		}
	}
}

// runOnce initiates new payouts, then reconciles already-pending payouts.
func (p *PayoutProcessor) runOnce(ctx context.Context) {
	p.initiateReadyPayouts(ctx)
	p.reconcilePendingPayouts(ctx)
}

func (p *PayoutProcessor) initiateReadyPayouts(ctx context.Context) {
	readyTrades, err := p.trades.GetPendingPayouts(ctx)
	if err != nil {
		p.logger.Error("failed to fetch payout-ready trades", "error", err)
		return
	}
	if len(readyTrades) == 0 {
		return
	}

	p.logger.Info("initiating payouts", "count", len(readyTrades))

	for _, trade := range readyTrades {
		if trade.BankAccID == nil {
			p.logger.Error("trade is missing bank account for payout", "trade_id", trade.ID.String(), "user_id", trade.UserID.String())
			continue
		}

		bankAccountID := trade.BankAccID.String()
		payoutAmount := trade.ToAmountExpected
		if trade.ToAmountActual != nil {
			payoutAmount = *trade.ToAmountActual
		}

		log := p.logger.With(
			"trade_id", trade.ID.String(),
			"user_id", trade.UserID.String(),
			"payout_amount", payoutAmount,
			"bank_account_id", bankAccountID,
		)

		log.Info("initiating payout via Graph Finance")

		payoutRef, err := p.graph.ConvertAndPay(ctx, bankAccountID, payoutAmount)
		if err != nil {
			if shouldFailPayoutPermanently(err) {
				if updateErr := p.trades.UpdateTradeStatus(ctx, trade.ID.String(), string(statemachine.TradePayoutFailed), map[string]interface{}{
					"reason":          "non-retryable payout initiation error: " + strings.TrimSpace(err.Error()),
					"idempotency_key": "payout_failed_initiation:" + trade.ID.String(),
				}); updateErr != nil {
					log.Error("failed to mark payout as failed after non-retryable initiation error", "error", err, "update_error", updateErr)
					continue
				}
				log.Warn("payout initiation failed with non-retryable error; marked payout failed", "error", err)
				continue
			}

			log.Error("payout initiation failed, will retry next tick", "error", err)
			continue
		}

		if err := p.trades.UpdateTradeStatus(ctx, trade.ID.String(), string(statemachine.TradePayoutPending), map[string]interface{}{
			"payout_ref":      payoutRef,
			"reason":          "graph payout initiated",
			"idempotency_key": "payout_pending:" + trade.ID.String() + ":" + strings.ToLower(strings.TrimSpace(payoutRef)),
		}); err != nil {
			log.Error("CRITICAL: payout was initiated but DB update failed", "payout_ref", payoutRef, "error", err)
			continue
		}

		log.Info("payout moved to pending", "payout_ref", payoutRef)
	}
}

func (p *PayoutProcessor) reconcilePendingPayouts(ctx context.Context) {
	pendingTrades, err := p.trades.GetTradesByStatus(ctx, string(statemachine.TradePayoutPending))
	if err != nil {
		p.logger.Error("failed to fetch pending payouts", "error", err)
		return
	}
	if len(pendingTrades) == 0 {
		return
	}

	for _, trade := range pendingTrades {
		if trade.GraphPayoutID == nil || strings.TrimSpace(*trade.GraphPayoutID) == "" {
			p.logger.Warn("pending payout trade has no payout reference", "trade_id", trade.ID.String())
			continue
		}

		payoutRef := strings.TrimSpace(*trade.GraphPayoutID)
		status, err := p.graph.GetPayoutStatus(ctx, payoutRef)
		if err != nil {
			p.logger.Error("failed to fetch payout status", "trade_id", trade.ID.String(), "payout_ref", payoutRef, "error", err)
			continue
		}

		normalized := normalizePayoutStatus(status)
		switch normalized {
		case "completed":
			if err := p.trades.MarkPayoutComplete(ctx, trade.ID.String(), payoutRef); err != nil {
				p.logger.Error("failed to mark payout complete", "trade_id", trade.ID.String(), "payout_ref", payoutRef, "error", err)
				continue
			}
			p.logger.Info("payout completed successfully", "trade_id", trade.ID.String(), "payout_ref", payoutRef)
		case "failed":
			if err := p.trades.UpdateTradeStatus(ctx, trade.ID.String(), string(statemachine.TradePayoutFailed), map[string]interface{}{
				"payout_ref":      payoutRef,
				"reason":          "graph payout failed with status " + status,
				"idempotency_key": "payout_failed:" + trade.ID.String() + ":" + strings.ToLower(strings.TrimSpace(payoutRef)),
			}); err != nil {
				p.logger.Error("failed to mark payout failed", "trade_id", trade.ID.String(), "payout_ref", payoutRef, "error", err)
				continue
			}
			p.logger.Warn("payout failed", "trade_id", trade.ID.String(), "payout_ref", payoutRef, "status", status)
		default:
			p.logger.Debug("payout still pending", "trade_id", trade.ID.String(), "payout_ref", payoutRef, "status", status)
		}
	}
}

func normalizePayoutStatus(status string) string {
	normalized := strings.ToUpper(strings.TrimSpace(status))
	switch {
	case strings.Contains(normalized, "SUCCESS"), strings.Contains(normalized, "COMPLETED"), strings.Contains(normalized, "PAID"):
		return "completed"
	case strings.Contains(normalized, "FAIL"), strings.Contains(normalized, "REJECT"), strings.Contains(normalized, "CANCEL"), strings.Contains(normalized, "REVERSE"):
		return "failed"
	default:
		return "pending"
	}
}

func shouldFailPayoutPermanently(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "invalid deposit amount") ||
		strings.Contains(message, "invalid payout amount") ||
		strings.Contains(message, "invalid destination") ||
		strings.Contains(message, "account not found") ||
		strings.Contains(message, "beneficiary")
}
