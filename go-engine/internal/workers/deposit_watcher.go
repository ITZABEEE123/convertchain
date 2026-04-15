package workers

import (
	"context"
	"log/slog"
	"time"

	"convert-chain/go-engine/internal/domain"
	"convert-chain/go-engine/internal/statemachine"
)

// DepositWatcher is a background worker that wakes up on an interval,
// fetches trades that are awaiting deposits or confirmations, and checks the
// blockchain for incoming funds.
//
// If a trade has been waiting too long in PENDING_DEPOSIT, the watcher
// cancels it. If funds have arrived, it advances the trade FSM one step at a
// time: PENDING_DEPOSIT -> DEPOSIT_RECEIVED -> DEPOSIT_CONFIRMED.
type DepositWatcher struct {
	trades     TradeRepository
	blockchain BlockchainClient
	tradeFSM   *statemachine.TradeFSM
	interval   time.Duration
	logger     *slog.Logger
}

// NewDepositWatcher constructs a DepositWatcher.
func NewDepositWatcher(
	trades TradeRepository,
	blockchain BlockchainClient,
	tradeFSM *statemachine.TradeFSM,
	interval time.Duration,
	logger *slog.Logger,
) *DepositWatcher {
	return &DepositWatcher{
		trades:     trades,
		blockchain: blockchain,
		tradeFSM:   tradeFSM,
		interval:   interval,
		logger:     logger,
	}
}

// Run starts the deposit watcher loop. It blocks until ctx is cancelled.
func (w *DepositWatcher) Run(ctx context.Context) {
	w.logger.Info("deposit watcher starting", "interval", w.interval)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	w.runOnce(ctx)

	for {
		select {
		case <-ticker.C:
			w.runOnce(ctx)
		case <-ctx.Done():
			w.logger.Info("deposit watcher shutting down")
			return
		}
	}
}

// runOnce performs one pass: fetch watched trades and check each one.
func (w *DepositWatcher) runOnce(ctx context.Context) {
	trades, err := w.getWatchedTrades(ctx)
	if err != nil {
		w.logger.Error("failed to fetch watched trades", "error", err)
		return
	}

	w.logger.Info("deposit watcher tick", "watched_count", len(trades))

	for _, trade := range trades {
		w.processTrade(ctx, trade)
	}
}

func (w *DepositWatcher) getWatchedTrades(ctx context.Context) ([]*domain.Trade, error) {
	pendingTrades, err := w.trades.GetTradesByStatus(ctx, string(statemachine.TradePendingDeposit))
	if err != nil {
		return nil, err
	}

	receivedTrades, err := w.trades.GetTradesByStatus(ctx, string(statemachine.TradeDepositReceived))
	if err != nil {
		return nil, err
	}

	trades := make([]*domain.Trade, 0, len(pendingTrades)+len(receivedTrades))
	trades = append(trades, pendingTrades...)
	trades = append(trades, receivedTrades...)
	return trades, nil
}

func (w *DepositWatcher) processTrade(ctx context.Context, trade *domain.Trade) {
	address := ""
	if trade.DepositAddress != nil {
		address = *trade.DepositAddress
	}

	log := w.logger.With("trade_id", trade.ID.String(), "status", trade.Status, "address", address)

	if trade.Status == string(statemachine.TradePendingDeposit) && time.Now().After(trade.ExpiresAt) {
		log.Warn("trade expired, cancelling")

		if err := w.tradeFSM.Transition(ctx, trade, statemachine.EventCancelled); err != nil {
			log.Error("FSM rejection on expire", "error", err)
			return
		}

		if err := w.trades.UpdateTradeStatus(ctx, trade.ID.String(), trade.Status, map[string]interface{}{
			"reason":     "deposit_deadline_exceeded",
			"expired_at": time.Now().UTC(),
		}); err != nil {
			log.Error("failed to persist cancellation", "error", err)
		}
		return
	}

	if address == "" {
		log.Warn("trade missing deposit address; skipping blockchain check")
		return
	}

	result, err := w.blockchain.CheckDeposit(ctx, trade.FromCurrency, address, trade.FromAmount)
	if err != nil {
		log.Error("blockchain check failed", "error", err)
		return
	}

	if !result.Found {
		log.Debug("no deposit detected yet")
		return
	}

	trade.Confirmations = result.Confirmations
	previousStatus := trade.Status

	switch trade.Status {
	case string(statemachine.TradePendingDeposit):
		if err := w.tradeFSM.Transition(ctx, trade, statemachine.EventDepositDetected); err != nil {
			log.Error("FSM rejected deposit detection",
				"current_status", previousStatus,
				"target_event", statemachine.EventDepositDetected,
				"error", err,
			)
			return
		}

		metadata := map[string]interface{}{
			"tx_hash":       result.TxHash,
			"amount":        result.AmountReceived,
			"confirmations": result.Confirmations,
			"detected_at":   time.Now().UTC(),
		}
		log.Info("deposit detected", "tx_hash", result.TxHash, "confirmations", result.Confirmations)

		if err := w.trades.UpdateTradeStatus(ctx, trade.ID.String(), trade.Status, metadata); err != nil {
			log.Error("failed to persist deposit status", "error", err)
		}

	case string(statemachine.TradeDepositReceived):
		if err := w.tradeFSM.Transition(ctx, trade, statemachine.EventDepositConfirmed); err != nil {
			log.Debug("deposit seen but not confirmable yet",
				"current_status", previousStatus,
				"confirmations", result.Confirmations,
				"error", err,
			)
			return
		}

		metadata := map[string]interface{}{
			"tx_hash":       result.TxHash,
			"amount":        result.AmountReceived,
			"confirmations": result.Confirmations,
			"confirmed_at":  time.Now().UTC(),
		}
		log.Info("deposit confirmed", "tx_hash", result.TxHash, "confirmations", result.Confirmations)

		if err := w.trades.UpdateTradeStatus(ctx, trade.ID.String(), trade.Status, metadata); err != nil {
			log.Error("failed to persist deposit status", "error", err)
		}

	default:
		log.Debug("trade status is not watched by deposit watcher")
	}
}
