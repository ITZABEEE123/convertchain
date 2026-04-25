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

const conversionReconciliationTimeout = 10 * time.Minute
const conversionMaxRetryAttempts = 6

// ConversionProcessor runs every few seconds and executes real sandbox/testnet
// exchange conversions for trades whose deposits have already been confirmed.
//
// Lifecycle:
//
//	DEPOSIT_CONFIRMED -> CONVERSION_IN_PROGRESS -> CONVERSION_COMPLETED
type ConversionProcessor struct {
	trades   TradeRepository
	executor ConversionExecutor
	interval time.Duration
	logger   *slog.Logger
	retries  map[string]int
}

func NewConversionProcessor(
	trades TradeRepository,
	executor ConversionExecutor,
	interval time.Duration,
	logger *slog.Logger,
) *ConversionProcessor {
	return &ConversionProcessor{
		trades:   trades,
		executor: executor,
		interval: interval,
		logger:   logger,
		retries:  map[string]int{},
	}
}

func (p *ConversionProcessor) Run(ctx context.Context) {
	p.logger.Info("conversion processor starting", "interval", p.interval)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	p.runOnce(ctx)

	for {
		select {
		case <-ticker.C:
			p.runOnce(ctx)
		case <-ctx.Done():
			p.logger.Info("conversion processor shutting down")
			return
		}
	}
}

func (p *ConversionProcessor) runOnce(ctx context.Context) {
	p.processConfirmedTrades(ctx)
	p.recoverInProgressTrades(ctx)
}

func (p *ConversionProcessor) processConfirmedTrades(ctx context.Context) {
	readyTrades, err := p.trades.GetTradesByStatus(ctx, string(statemachine.TradeDepositConfirmed))
	if err != nil {
		p.logger.Error("failed to fetch conversion-ready trades", "error", err)
		return
	}
	if len(readyTrades) == 0 {
		return
	}

	p.logger.Info("processing exchange conversions", "count", len(readyTrades))

	for _, trade := range readyTrades {
		p.processTrade(ctx, trade)
	}
}

func (p *ConversionProcessor) recoverInProgressTrades(ctx context.Context) {
	inProgressTrades, err := p.trades.GetTradesByStatus(ctx, string(statemachine.TradeConversionInProgress))
	if err != nil {
		p.logger.Error("failed to fetch in-progress conversions", "error", err)
		return
	}
	if len(inProgressTrades) == 0 {
		return
	}

	for _, trade := range inProgressTrades {
		if time.Since(trade.UpdatedAt) < conversionReconciliationTimeout {
			continue
		}

		metadata := map[string]interface{}{
			"reason":          "conversion reconciliation timed out; manual review required",
			"idempotency_key": fmt.Sprintf("conversion_reconcile_timeout:%s", trade.ID.String()),
		}
		if trade.ExchangeOrderID != nil && strings.TrimSpace(*trade.ExchangeOrderID) != "" {
			metadata["exchange_order_id"] = strings.TrimSpace(*trade.ExchangeOrderID)
			metadata["idempotency_key"] = fmt.Sprintf("conversion_reconcile_timeout:%s:%s", trade.ID.String(), strings.ToLower(strings.TrimSpace(*trade.ExchangeOrderID)))
		}

		if err := p.trades.UpdateTradeStatus(ctx, trade.ID.String(), string(statemachine.TradeDispute), metadata); err != nil {
			p.logger.Error("failed to escalate stale conversion", "trade_id", trade.ID.String(), "error", err)
			continue
		}
		p.clearRetryAttempt(trade.ID.String())

		p.logger.Warn("conversion left in dispute after stale in-progress state", "trade_id", trade.ID.String(), "age", time.Since(trade.UpdatedAt))
	}
}

func (p *ConversionProcessor) processTrade(ctx context.Context, trade *domain.Trade) {
	log := p.logger.With(
		"trade_id", trade.ID.String(),
		"user_id", trade.UserID.String(),
		"asset", trade.FromCurrency,
		"amount_minor", trade.FromAmount,
	)

	result, err := p.executor.ConvertToStable(ctx, trade.FromCurrency, trade.FromAmount)
	if err != nil {
		if shouldEscalateConversionError(err) {
			metadata := map[string]interface{}{
				"reason":          fmt.Sprintf("conversion requires manual review: %s", strings.TrimSpace(err.Error())),
				"idempotency_key": fmt.Sprintf("conversion_escalation:%s", trade.ID.String()),
			}
			if updateErr := p.trades.UpdateTradeStatus(ctx, trade.ID.String(), string(statemachine.TradeDispute), metadata); updateErr != nil {
				log.Error("conversion failed and dispute escalation also failed", "error", err, "update_error", updateErr)
				return
			}

			log.Warn("conversion failed with non-retryable error; moved to dispute", "error", err)
			p.clearRetryAttempt(trade.ID.String())
			return
		}

		attempt := p.incrementRetryAttempt(trade.ID.String())
		if attempt >= conversionMaxRetryAttempts {
			metadata := map[string]interface{}{
				"reason":          fmt.Sprintf("conversion retry limit reached after %d attempts: %s", attempt, strings.TrimSpace(err.Error())),
				"idempotency_key": fmt.Sprintf("conversion_retries_exhausted:%s", trade.ID.String()),
			}
			if updateErr := p.trades.UpdateTradeStatus(ctx, trade.ID.String(), string(statemachine.TradeDispute), metadata); updateErr != nil {
				log.Error("conversion retry limit reached but dispute escalation failed", "attempt", attempt, "error", err, "update_error", updateErr)
				return
			}

			log.Warn("conversion retries exhausted; moved to dispute", "attempt", attempt, "error", err)
			p.clearRetryAttempt(trade.ID.String())
			return
		}

		log.Error("conversion failed, will retry next tick", "error", err, "attempt", attempt, "max_attempts", conversionMaxRetryAttempts)
		return
	}
	p.clearRetryAttempt(trade.ID.String())

	startNote := fmt.Sprintf("%s conversion order accepted", strings.ToLower(strings.TrimSpace(result.Exchange)))
	if err := p.trades.UpdateTradeStatus(ctx, trade.ID.String(), string(statemachine.TradeConversionInProgress), map[string]interface{}{
		"exchange_order_id": strings.TrimSpace(result.OrderID),
		"reason":            startNote,
		"idempotency_key":   fmt.Sprintf("conversion_started:%s:%s", trade.ID.String(), strings.ToLower(strings.TrimSpace(result.OrderID))),
	}); err != nil {
		log.Error("CRITICAL: exchange conversion executed but DB update failed", "order_id", result.OrderID, "error", err)
		return
	}

	finishNote := fmt.Sprintf("%s conversion %s", strings.ToLower(strings.TrimSpace(result.Exchange)), normalizeConversionStatus(result.Status))
	switch conversionStatusDisposition(result.Status) {
	case "terminal":
		if err := p.trades.UpdateTradeStatus(ctx, trade.ID.String(), string(statemachine.TradeConversionCompleted), map[string]interface{}{
			"exchange_order_id": strings.TrimSpace(result.OrderID),
			"reason":            finishNote,
			"idempotency_key":   fmt.Sprintf("conversion_completed:%s:%s", trade.ID.String(), strings.ToLower(strings.TrimSpace(result.OrderID))),
		}); err != nil {
			log.Error("failed to mark conversion completed", "order_id", result.OrderID, "error", err)
			return
		}

		log.Info(
			"conversion completed",
			"exchange", result.Exchange,
			"symbol", result.Symbol,
			"order_id", result.OrderID,
			"status", result.Status,
			"executed_qty", result.ExecutedQty,
			"quote_qty", result.QuoteQty,
		)
	case "failed":
		if err := p.trades.UpdateTradeStatus(ctx, trade.ID.String(), string(statemachine.TradeDispute), map[string]interface{}{
			"exchange_order_id": strings.TrimSpace(result.OrderID),
			"reason":            finishNote,
			"idempotency_key":   fmt.Sprintf("conversion_failed_dispute:%s:%s", trade.ID.String(), strings.ToLower(strings.TrimSpace(result.OrderID))),
		}); err != nil {
			log.Error("failed to move failed conversion to dispute", "order_id", result.OrderID, "error", err)
			return
		}

		log.Warn("conversion returned non-terminal failure status; moved to dispute", "status", result.Status, "order_id", result.OrderID)
	default:
		log.Info(
			"conversion accepted and awaiting reconciliation",
			"exchange", result.Exchange,
			"symbol", result.Symbol,
			"order_id", result.OrderID,
			"status", result.Status,
			"executed_qty", result.ExecutedQty,
			"quote_qty", result.QuoteQty,
		)
	}
}

func normalizeConversionStatus(status string) string {
	normalized := strings.ToUpper(strings.TrimSpace(status))
	switch {
	case normalized == "":
		return "completed"
	case strings.Contains(normalized, "FILL"), strings.Contains(normalized, "DONE"), strings.Contains(normalized, "COMPLETE"):
		return "filled"
	case strings.Contains(normalized, "SUBMIT"):
		return "submitted"
	case strings.Contains(normalized, "NOOP"):
		return "noop"
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}

func conversionStatusDisposition(status string) string {
	normalized := strings.ToUpper(strings.TrimSpace(status))
	switch {
	case normalized == "NOOP":
		return "terminal"
	case strings.Contains(normalized, "FILL"), strings.Contains(normalized, "DONE"), strings.Contains(normalized, "COMPLETE"):
		return "terminal"
	case strings.Contains(normalized, "SUBMIT"), strings.Contains(normalized, "NEW"), strings.Contains(normalized, "PEND"), strings.Contains(normalized, "PARTIAL"), strings.Contains(normalized, "PROCESS"):
		return "in_progress"
	case strings.Contains(normalized, "FAIL"), strings.Contains(normalized, "REJECT"), strings.Contains(normalized, "CANCEL"), strings.Contains(normalized, "EXPIRE"):
		return "failed"
	default:
		return "in_progress"
	}
}

func shouldEscalateConversionError(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "insufficient balance") ||
		strings.Contains(message, "api keys not configured") ||
		strings.Contains(message, "unsupported conversion asset") ||
		strings.Contains(message, "conversion quantity resolved to zero")
}

func (p *ConversionProcessor) incrementRetryAttempt(tradeID string) int {
	p.retries[tradeID] = p.retries[tradeID] + 1
	return p.retries[tradeID]
}

func (p *ConversionProcessor) clearRetryAttempt(tradeID string) {
	delete(p.retries, tradeID)
}
