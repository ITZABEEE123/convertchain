package workers

import (
	"context"
	"log/slog"
	"time"
)

// PayoutProcessor runs every 30 seconds and processes trades that have
// reached DEPOSIT_CONFIRMED status. It calls Graph Finance to convert funds
// and initiate a bank payout for the trade's configured destination account.
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

// runOnce processes every trade that is ready for payout.
func (p *PayoutProcessor) runOnce(ctx context.Context) {
	readyTrades, err := p.trades.GetPendingPayouts(ctx)
	if err != nil {
		p.logger.Error("failed to fetch payout-ready trades", "error", err)
		return
	}

	if len(readyTrades) == 0 {
		return
	}

	p.logger.Info("processing payouts", "count", len(readyTrades))

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
			// A failed payout does NOT change the trade status.
			// The trade stays in DEPOSIT_CONFIRMED so the next tick retries it.
			log.Error("payout failed, will retry next tick", "error", err)
			continue
		}

		if err := p.trades.MarkPayoutComplete(ctx, trade.ID.String(), payoutRef); err != nil {
			// CRITICAL: Graph Finance accepted the payout but we failed to record it.
			// Reconciliation will be needed.
			log.Error("CRITICAL: payout succeeded but DB update failed",
				"payout_ref", payoutRef,
				"error", err,
			)
			continue
		}

		log.Info("payout completed successfully", "payout_ref", payoutRef)
	}
}
