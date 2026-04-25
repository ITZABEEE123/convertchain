package workers

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
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
	policies   DepositPolicySet
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
	return NewDepositWatcherWithPolicy(trades, blockchain, tradeFSM, DefaultDepositPolicySet(), interval, logger)
}

func NewDepositWatcherWithPolicy(
	trades TradeRepository,
	blockchain BlockchainClient,
	tradeFSM *statemachine.TradeFSM,
	policies DepositPolicySet,
	interval time.Duration,
	logger *slog.Logger,
) *DepositWatcher {
	return &DepositWatcher{
		trades:     trades,
		blockchain: blockchain,
		tradeFSM:   tradeFSM,
		policies:   policies,
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
			"reason":          "deposit_deadline_exceeded",
			"expired_at":      time.Now().UTC(),
			"idempotency_key": fmt.Sprintf("trade_expired:%s", trade.ID.String()),
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

	expectedNetwork, expectedAddress := parseExpectedDepositNetworkAndAddress(trade.FromCurrency, address)
	policy, ok := w.policies.Resolve(trade.FromCurrency, expectedNetwork)
	if !ok {
		policy = DepositConfirmationPolicy{
			Currency:               strings.ToUpper(strings.TrimSpace(trade.FromCurrency)),
			Network:                expectedNetwork,
			DetectionConfirmations: 1,
			FinalityConfirmations:  1,
			AmountToleranceMinor:   0,
		}
	}

	txHash := strings.TrimSpace(result.TxHash)
	if txHash != "" {
		existingTrade, err := w.trades.GetTradeByDepositTxHash(ctx, txHash)
		if err != nil {
			log.Error("failed duplicate tx hash lookup", "tx_hash", txHash, "error", err)
			return
		}
		if existingTrade != nil && existingTrade.ID != trade.ID {
			w.moveToDispute(ctx, trade, "duplicate_deposit_tx_hash", map[string]interface{}{
				"tx_hash":          txHash,
				"existing_trade":   existingTrade.ID.String(),
				"observed_network": normalizeNetworkName(result.Network),
			})
			log.Warn("duplicate deposit tx hash detected", "tx_hash", txHash, "existing_trade_id", existingTrade.ID.String())
			return
		}
	}

	observedNetwork := normalizeNetworkName(result.Network)
	if observedNetwork != "" && observedNetwork != "default" && expectedNetwork != "default" && observedNetwork != expectedNetwork {
		w.moveToDispute(ctx, trade, "wrong_deposit_network", map[string]interface{}{
			"tx_hash":          txHash,
			"expected_network": expectedNetwork,
			"observed_network": observedNetwork,
		})
		log.Warn("deposit network mismatch", "expected_network", expectedNetwork, "observed_network", observedNetwork, "tx_hash", txHash)
		return
	}

	if strings.TrimSpace(result.Address) != "" && strings.TrimSpace(expectedAddress) != "" && !strings.EqualFold(strings.TrimSpace(result.Address), strings.TrimSpace(expectedAddress)) {
		w.moveToDispute(ctx, trade, "wrong_deposit_address", map[string]interface{}{
			"tx_hash":           txHash,
			"expected_address":  expectedAddress,
			"observed_address":  strings.TrimSpace(result.Address),
			"expected_network":  expectedNetwork,
			"observed_network":  observedNetwork,
			"reorg_risk":        result.ReorgRisk,
			"replacement_risk":  result.Replaced,
			"reversal_detected": result.Reversed,
		})
		log.Warn("deposit address mismatch", "expected_address", expectedAddress, "observed_address", result.Address, "tx_hash", txHash)
		return
	}

	riskAddress := strings.TrimSpace(result.Address)
	if riskAddress == "" {
		riskAddress = strings.TrimSpace(expectedAddress)
	}
	if isHighRiskWalletAddress(riskAddress) {
		w.moveToDispute(ctx, trade, "high_risk_wallet_blocklist", map[string]interface{}{
			"tx_hash":          txHash,
			"risk_address":     riskAddress,
			"expected_network": expectedNetwork,
			"observed_network": observedNetwork,
		})
		log.Warn("deposit wallet address blocked by high-risk controls", "risk_address", riskAddress, "tx_hash", txHash)
		return
	}

	if amountOutsideTolerance(trade.FromAmount, result.AmountReceived, policy.AmountToleranceMinor) {
		w.moveToDispute(ctx, trade, "wrong_deposit_amount", map[string]interface{}{
			"tx_hash":           txHash,
			"expected_amount":   trade.FromAmount,
			"observed_amount":   result.AmountReceived,
			"amount_tolerance":  policy.AmountToleranceMinor,
			"expected_network":  expectedNetwork,
			"observed_network":  observedNetwork,
			"reorg_risk":        result.ReorgRisk,
			"replacement_risk":  result.Replaced,
			"reversal_detected": result.Reversed,
		})
		log.Warn("deposit amount mismatch", "expected_amount", trade.FromAmount, "observed_amount", result.AmountReceived, "tolerance", policy.AmountToleranceMinor, "tx_hash", txHash)
		return
	}

	if result.Reversed || result.Replaced {
		w.moveToDispute(ctx, trade, "deposit_reorg_or_replacement_risk", map[string]interface{}{
			"tx_hash":           txHash,
			"expected_network":  expectedNetwork,
			"observed_network":  observedNetwork,
			"replacement_risk":  result.Replaced,
			"reversal_detected": result.Reversed,
		})
		log.Warn("deposit flagged for reorg/replacement risk", "tx_hash", txHash, "replacement_risk", result.Replaced, "reversal_detected", result.Reversed)
		return
	}

	if result.Confirmations < policy.DetectionConfirmations {
		log.Info("deposit seen but below detection confirmation threshold",
			"tx_hash", txHash,
			"confirmations", result.Confirmations,
			"detection_confirmations", policy.DetectionConfirmations,
			"network", firstNonEmptyNetwork(observedNetwork, expectedNetwork),
		)
		return
	}

	trade.Confirmations = result.Confirmations
	trade.RequiredConfirmations = policy.FinalityConfirmations
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
			"tx_hash":         result.TxHash,
			"amount":          result.AmountReceived,
			"confirmations":   result.Confirmations,
			"detected_at":     time.Now().UTC(),
			"network":         firstNonEmptyNetwork(observedNetwork, expectedNetwork),
			"reorg_risk":      result.ReorgRisk,
			"idempotency_key": fmt.Sprintf("deposit_detected:%s:%s", trade.ID.String(), strings.ToLower(strings.TrimSpace(result.TxHash))),
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
			"tx_hash":         result.TxHash,
			"amount":          result.AmountReceived,
			"confirmations":   result.Confirmations,
			"confirmed_at":    time.Now().UTC(),
			"network":         firstNonEmptyNetwork(observedNetwork, expectedNetwork),
			"reorg_risk":      result.ReorgRisk,
			"idempotency_key": fmt.Sprintf("deposit_confirmed:%s:%s", trade.ID.String(), strings.ToLower(strings.TrimSpace(result.TxHash))),
		}
		log.Info("deposit confirmed", "tx_hash", result.TxHash, "confirmations", result.Confirmations)

		if err := w.trades.UpdateTradeStatus(ctx, trade.ID.String(), trade.Status, metadata); err != nil {
			log.Error("failed to persist deposit status", "error", err)
		}

	default:
		log.Debug("trade status is not watched by deposit watcher")
	}
}

func firstNonEmptyNetwork(values ...string) string {
	for _, value := range values {
		normalized := normalizeNetworkName(value)
		if normalized != "" && normalized != "default" {
			return normalized
		}
	}
	return "default"
}

func (w *DepositWatcher) moveToDispute(ctx context.Context, trade *domain.Trade, reason string, metadata map[string]interface{}) {
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	metadata["reason"] = reason
	metadata["skip_ledger_posting"] = true
	if txHash, ok := metadata["tx_hash"].(string); ok && strings.TrimSpace(txHash) != "" {
		metadata["idempotency_key"] = fmt.Sprintf("deposit_dispute:%s:%s", trade.ID.String(), strings.ToLower(strings.TrimSpace(txHash)))
	} else {
		metadata["idempotency_key"] = fmt.Sprintf("deposit_dispute:%s:%s", trade.ID.String(), strings.ToLower(strings.TrimSpace(reason)))
	}

	if err := w.trades.UpdateTradeStatus(ctx, trade.ID.String(), string(statemachine.TradeDispute), metadata); err != nil {
		w.logger.Error("failed to move trade to dispute", "trade_id", trade.ID.String(), "reason", reason, "error", err)
	}
}

func isHighRiskWalletAddress(address string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(address))
	if normalized == "" {
		return false
	}
	for _, raw := range strings.Split(strings.TrimSpace(os.Getenv("HIGH_RISK_WALLET_BLOCKLIST")), ",") {
		candidate := strings.ToUpper(strings.TrimSpace(raw))
		if candidate == "" {
			continue
		}
		if candidate == normalized {
			return true
		}
	}
	return false
}
