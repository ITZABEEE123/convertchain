package workers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"convert-chain/go-engine/internal/domain"
	"convert-chain/go-engine/internal/statemachine"
)

type DepositBackfillScanner struct {
	trades     TradeRepository
	blockchain BlockchainClient
	policies   DepositPolicySet
	interval   time.Duration
	logger     *slog.Logger
}

func NewDepositBackfillScanner(
	trades TradeRepository,
	blockchain BlockchainClient,
	policies DepositPolicySet,
	interval time.Duration,
	logger *slog.Logger,
) *DepositBackfillScanner {
	return &DepositBackfillScanner{
		trades:     trades,
		blockchain: blockchain,
		policies:   policies,
		interval:   interval,
		logger:     logger,
	}
}

func (s *DepositBackfillScanner) Run(ctx context.Context) {
	s.logger.Info("deposit backfill scanner starting", "interval", s.interval)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.runOnce(ctx)

	for {
		select {
		case <-ticker.C:
			s.runOnce(ctx)
		case <-ctx.Done():
			s.logger.Info("deposit backfill scanner shutting down")
			return
		}
	}
}

func (s *DepositBackfillScanner) runOnce(ctx context.Context) {
	watched := []string{
		string(statemachine.TradePendingDeposit),
		string(statemachine.TradeDepositReceived),
		string(statemachine.TradeDepositConfirmed),
		string(statemachine.TradeConversionInProgress),
		string(statemachine.TradeConversionCompleted),
		string(statemachine.TradePayoutPending),
	}

	for _, status := range watched {
		trades, err := s.trades.GetTradesByStatus(ctx, status)
		if err != nil {
			s.logger.Error("backfill failed to fetch trades", "status", status, "error", err)
			continue
		}
		for _, trade := range trades {
			s.scanTrade(ctx, trade)
		}
	}
}

func (s *DepositBackfillScanner) scanTrade(ctx context.Context, trade *domain.Trade) {
	if trade == nil || trade.DepositAddress == nil || strings.TrimSpace(*trade.DepositAddress) == "" {
		return
	}

	expectedNetwork, expectedAddress := parseExpectedDepositNetworkAndAddress(trade.FromCurrency, strings.TrimSpace(*trade.DepositAddress))
	policy, ok := s.policies.Resolve(trade.FromCurrency, expectedNetwork)
	if !ok {
		policy = DepositConfirmationPolicy{
			Currency:               strings.ToUpper(strings.TrimSpace(trade.FromCurrency)),
			Network:                expectedNetwork,
			DetectionConfirmations: 1,
			FinalityConfirmations:  1,
			AmountToleranceMinor:   0,
		}
	}

	result, err := s.blockchain.CheckDeposit(ctx, trade.FromCurrency, strings.TrimSpace(*trade.DepositAddress), trade.FromAmount)
	if err != nil {
		s.logger.Error("backfill blockchain check failed", "trade_id", trade.ID.String(), "error", err)
		return
	}
	if !result.Found {
		return
	}

	txHash := strings.TrimSpace(result.TxHash)
	if txHash != "" {
		existingTrade, err := s.trades.GetTradeByDepositTxHash(ctx, txHash)
		if err != nil {
			s.logger.Error("backfill duplicate tx lookup failed", "trade_id", trade.ID.String(), "tx_hash", txHash, "error", err)
			return
		}
		if existingTrade != nil && existingTrade.ID != trade.ID {
			s.raiseDispute(ctx, trade, "backfill_duplicate_deposit_tx_hash", map[string]interface{}{
				"tx_hash": txHash,
			})
			return
		}
	}

	network := normalizeNetworkName(result.Network)
	if network != "" && network != "default" && expectedNetwork != "default" && network != expectedNetwork {
		s.raiseDispute(ctx, trade, "backfill_wrong_deposit_network", map[string]interface{}{
			"tx_hash":          txHash,
			"expected_network": expectedNetwork,
			"observed_network": network,
		})
		return
	}
	if strings.TrimSpace(result.Address) != "" && strings.TrimSpace(expectedAddress) != "" && !strings.EqualFold(strings.TrimSpace(result.Address), strings.TrimSpace(expectedAddress)) {
		s.raiseDispute(ctx, trade, "backfill_wrong_deposit_address", map[string]interface{}{
			"tx_hash":          txHash,
			"expected_address": expectedAddress,
			"observed_address": strings.TrimSpace(result.Address),
			"expected_network": expectedNetwork,
			"observed_network": network,
		})
		return
	}
	if amountOutsideTolerance(trade.FromAmount, result.AmountReceived, policy.AmountToleranceMinor) {
		s.raiseDispute(ctx, trade, "backfill_wrong_deposit_amount", map[string]interface{}{
			"tx_hash":          txHash,
			"expected_amount":  trade.FromAmount,
			"observed_amount":  result.AmountReceived,
			"amount_tolerance": policy.AmountToleranceMinor,
			"expected_network": expectedNetwork,
			"observed_network": network,
		})
		return
	}
	if result.Reversed || result.Replaced {
		s.raiseDispute(ctx, trade, "backfill_detected_reorg_or_replacement", map[string]interface{}{
			"tx_hash":           txHash,
			"replacement_risk":  result.Replaced,
			"reversal_detected": result.Reversed,
			"network":           firstNonEmptyNetwork(network, expectedNetwork),
		})
		return
	}

	switch trade.Status {
	case string(statemachine.TradePendingDeposit):
		if result.Confirmations >= policy.DetectionConfirmations {
			_ = s.trades.UpdateTradeStatus(ctx, trade.ID.String(), string(statemachine.TradeDepositReceived), map[string]interface{}{
				"tx_hash":         txHash,
				"amount":          result.AmountReceived,
				"confirmations":   result.Confirmations,
				"network":         firstNonEmptyNetwork(network, expectedNetwork),
				"reason":          "backfill detected deposit",
				"idempotency_key": fmt.Sprintf("backfill_deposit_detected:%s:%s", trade.ID.String(), strings.ToLower(txHash)),
			})
		}

	case string(statemachine.TradeDepositReceived):
		if result.Confirmations >= policy.FinalityConfirmations {
			_ = s.trades.UpdateTradeStatus(ctx, trade.ID.String(), string(statemachine.TradeDepositConfirmed), map[string]interface{}{
				"tx_hash":         txHash,
				"amount":          result.AmountReceived,
				"confirmations":   result.Confirmations,
				"network":         firstNonEmptyNetwork(network, expectedNetwork),
				"reason":          "backfill confirmed deposit",
				"idempotency_key": fmt.Sprintf("backfill_deposit_confirmed:%s:%s", trade.ID.String(), strings.ToLower(txHash)),
			})
		}

	case string(statemachine.TradeDepositConfirmed), string(statemachine.TradeConversionInProgress), string(statemachine.TradeConversionCompleted), string(statemachine.TradePayoutPending):
		if result.Confirmations < policy.FinalityConfirmations {
			s.raiseDispute(ctx, trade, "finality_dropped_below_threshold", map[string]interface{}{
				"tx_hash":                txHash,
				"confirmations":          result.Confirmations,
				"finality_confirmations": policy.FinalityConfirmations,
				"network":                firstNonEmptyNetwork(network, expectedNetwork),
			})
		}
	}
}

func (s *DepositBackfillScanner) raiseDispute(ctx context.Context, trade *domain.Trade, reason string, metadata map[string]interface{}) {
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	metadata["reason"] = reason
	metadata["skip_ledger_posting"] = true
	if txHash, ok := metadata["tx_hash"].(string); ok && strings.TrimSpace(txHash) != "" {
		metadata["idempotency_key"] = fmt.Sprintf("backfill_dispute:%s:%s", trade.ID.String(), strings.ToLower(strings.TrimSpace(txHash)))
	} else {
		metadata["idempotency_key"] = fmt.Sprintf("backfill_dispute:%s:%s", trade.ID.String(), strings.ToLower(strings.TrimSpace(reason)))
	}

	if err := s.trades.UpdateTradeStatus(ctx, trade.ID.String(), string(statemachine.TradeDispute), metadata); err != nil {
		s.logger.Error("failed to raise dispute from backfill", "trade_id", trade.ID.String(), "reason", reason, "error", err)
	}
}
