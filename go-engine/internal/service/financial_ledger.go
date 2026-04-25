package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"convert-chain/go-engine/internal/domain"
	"convert-chain/go-engine/internal/statemachine"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func normalizeMoneyOperationKey(raw string) string {
	key := strings.TrimSpace(raw)
	if key == "" {
		return ""
	}
	key = strings.ToLower(key)
	key = strings.ReplaceAll(key, " ", "_")
	if len(key) > 255 {
		key = key[:255]
	}
	return key
}

func metadataInt64(metadata map[string]interface{}, keys ...string) int64 {
	for _, key := range keys {
		v, ok := metadata[key]
		if !ok || v == nil {
			continue
		}
		switch typed := v.(type) {
		case int:
			return int64(typed)
		case int32:
			return int64(typed)
		case int64:
			return typed
		case float32:
			return int64(typed)
		case float64:
			return int64(typed)
		case string:
			parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
			if err == nil {
				return parsed
			}
		}
	}
	return 0
}

func buildTradeOperationKey(trade *domain.Trade, status string, metadata map[string]interface{}) string {
	if metadata != nil {
		if raw, ok := metadata["idempotency_key"]; ok {
			if key, ok := raw.(string); ok {
				normalized := normalizeMoneyOperationKey(key)
				if normalized != "" {
					return normalized
				}
			}
		}
	}

	parts := []string{"trade", strings.ToLower(strings.TrimSpace(status))}
	if trade != nil {
		parts = append(parts, trade.ID.String())
	}

	if metadata != nil {
		for _, key := range []string{"tx_hash", "payout_ref", "exchange_order_id", "graph_conversion_id", "reason"} {
			if raw, ok := metadata[key]; ok && raw != nil {
				parts = append(parts, fmt.Sprintf("%s=%v", key, raw))
			}
		}
	}
	if len(parts) == 3 {
		parts = append(parts, "default")
	}
	return normalizeMoneyOperationKey(strings.Join(parts, ":"))
}

func isAllowedTradeStatusTransition(fromStatus string, toStatus string) bool {
	if strings.EqualFold(strings.TrimSpace(fromStatus), strings.TrimSpace(toStatus)) {
		return true
	}

	allowed := map[string]map[string]bool{
		string(statemachine.TradeQuoteProvided): {
			string(statemachine.TradePendingDeposit): true,
			string(statemachine.TradeQuoteExpired):   true,
		},
		string(statemachine.TradePendingDeposit): {
			string(statemachine.TradeDepositReceived): true,
			string(statemachine.TradeCancelled):       true,
			string(statemachine.TradeDispute):         true,
		},
		string(statemachine.TradeDepositReceived): {
			string(statemachine.TradeDepositConfirmed): true,
			string(statemachine.TradeDispute):          true,
		},
		string(statemachine.TradeDepositConfirmed): {
			string(statemachine.TradeConversionInProgress): true,
			string(statemachine.TradeDispute):              true,
		},
		string(statemachine.TradeConversionInProgress): {
			string(statemachine.TradeConversionCompleted): true,
			string(statemachine.TradeDispute):             true,
		},
		string(statemachine.TradeConversionCompleted): {
			string(statemachine.TradePayoutPending): true,
			string(statemachine.TradeDispute):       true,
		},
		string(statemachine.TradePayoutPending): {
			string(statemachine.TradePayoutCompleted): true,
			string(statemachine.TradePayoutFailed):    true,
			string(statemachine.TradeDispute):         true,
		},
		string(statemachine.TradePayoutFailed): {
			string(statemachine.TradePayoutPending): true,
			string(statemachine.TradeDispute):       true,
		},
		string(statemachine.TradeDispute): {
			string(statemachine.TradeDepositConfirmed):    true,
			string(statemachine.TradeConversionCompleted): true,
			string(statemachine.TradePayoutCompleted):     true,
			string(statemachine.TradeDisputeClosed):       true,
		},
	}

	from := strings.ToUpper(strings.TrimSpace(fromStatus))
	to := strings.ToUpper(strings.TrimSpace(toStatus))
	targets, ok := allowed[from]
	if !ok {
		return false
	}
	return targets[to]
}

func (s *ApplicationService) reserveMoneyOperationKeyTx(
	ctx context.Context,
	tx pgx.Tx,
	scope string,
	operationKey string,
	tradeID uuid.UUID,
) (bool, error) {
	scope = strings.TrimSpace(scope)
	operationKey = normalizeMoneyOperationKey(operationKey)
	if scope == "" || operationKey == "" {
		return false, errors.New("missing idempotency scope or key")
	}

	var tradeIDValue interface{}
	if tradeID != uuid.Nil {
		tradeIDValue = tradeID
	}

	var inserted bool
	err := tx.QueryRow(ctx, `
        INSERT INTO financial_operation_keys (scope, operation_key, trade_id)
        VALUES ($1, $2, $3)
        ON CONFLICT (scope, operation_key) DO NOTHING
        RETURNING TRUE
    `, scope, operationKey, tradeIDValue).Scan(&inserted)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return inserted, nil
}

func (s *ApplicationService) ledgerBalanceForAccountTx(
	ctx context.Context,
	tx pgx.Tx,
	accountRef string,
	currency string,
) (int64, error) {
	var balance int64
	err := tx.QueryRow(ctx, `
        SELECT balance_after
        FROM ledger_entries
        WHERE account_ref = $1
          AND currency = $2
        ORDER BY created_at DESC, id DESC
        LIMIT 1
    `, accountRef, strings.ToUpper(strings.TrimSpace(currency))).Scan(&balance)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return balance, nil
}

func copyMetadata(metadata map[string]interface{}) map[string]interface{} {
	if len(metadata) == 0 {
		return map[string]interface{}{}
	}
	copied := make(map[string]interface{}, len(metadata))
	for k, v := range metadata {
		copied[k] = v
	}
	return copied
}

func (s *ApplicationService) postBalancedLedgerEntryTx(
	ctx context.Context,
	tx pgx.Tx,
	tradeID uuid.UUID,
	entryType string,
	currency string,
	amount int64,
	debitAccount string,
	creditAccount string,
	idempotencyKey string,
	metadata map[string]interface{},
) error {
	if amount <= 0 {
		return nil
	}

	normalizedCurrency := strings.ToUpper(strings.TrimSpace(currency))
	normalizedEntryType := strings.ToUpper(strings.TrimSpace(entryType))
	if normalizedCurrency == "" || normalizedEntryType == "" {
		return errors.New("entry_type and currency are required")
	}

	debitBefore, err := s.ledgerBalanceForAccountTx(ctx, tx, debitAccount, normalizedCurrency)
	if err != nil {
		return err
	}
	creditBefore, err := s.ledgerBalanceForAccountTx(ctx, tx, creditAccount, normalizedCurrency)
	if err != nil {
		return err
	}

	payload := copyMetadata(metadata)
	payload["debit_account"] = debitAccount
	payload["credit_account"] = creditAccount
	payload["amount"] = amount
	payload["currency"] = normalizedCurrency
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	debitKey := normalizeMoneyOperationKey(idempotencyKey + ":debit")
	creditKey := normalizeMoneyOperationKey(idempotencyKey + ":credit")

	_, err = tx.Exec(ctx, `
        INSERT INTO ledger_entries (
            trade_id,
            entry_type,
            currency,
            amount,
            direction,
            account_ref,
            balance_after,
            idempotency_key,
            metadata
        ) VALUES
            ($1::uuid, $2, $3, $4, 'D', $5, $6, $7, $8::jsonb),
            ($1::uuid, $2, $3, $4, 'C', $9, $10, $11, $8::jsonb)
    `,
		tradeID,
		normalizedEntryType,
		normalizedCurrency,
		amount,
		debitAccount,
		debitBefore+amount,
		debitKey,
		string(payloadBytes),
		creditAccount,
		creditBefore-amount,
		creditKey,
	)
	return err
}

func statusLedgerScope(status string) string {
	return "ledger_posting:trade_status:" + strings.ToLower(strings.TrimSpace(status))
}

func resolvePayoutAmount(trade *domain.Trade, metadata map[string]interface{}) int64 {
	amount := metadataInt64(metadata, "to_amount_actual", "payout_amount")
	if amount > 0 {
		return amount
	}
	if trade != nil && trade.ToAmountActual != nil && *trade.ToAmountActual > 0 {
		return *trade.ToAmountActual
	}
	if trade != nil {
		return trade.ToAmountExpected
	}
	return 0
}

func stableMetadataFingerprint(metadata map[string]interface{}) string {
	if len(metadata) == 0 {
		return "default"
	}
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", key, metadata[key]))
	}
	return strings.Join(parts, "|")
}

func (s *ApplicationService) postLedgerForStatusTransitionTx(
	ctx context.Context,
	tx pgx.Tx,
	trade *domain.Trade,
	status string,
	metadata map[string]interface{},
	operationKey string,
) error {
	if trade == nil {
		return nil
	}
	status = strings.ToUpper(strings.TrimSpace(status))

	switch status {
	case string(statemachine.TradeDepositConfirmed):
		return s.postBalancedLedgerEntryTx(
			ctx,
			tx,
			trade.ID,
			"DEPOSIT",
			trade.FromCurrency,
			trade.FromAmount,
			"platform:holding:"+strings.ToUpper(trade.FromCurrency),
			"external:blockchain:"+strings.ToUpper(trade.FromCurrency),
			operationKey+":deposit",
			metadata,
		)

	case string(statemachine.TradeConversionCompleted):
		if err := s.postBalancedLedgerEntryTx(
			ctx,
			tx,
			trade.ID,
			"CONVERSION_ASSET",
			trade.FromCurrency,
			trade.FromAmount,
			"exchange:inventory:"+strings.ToUpper(trade.FromCurrency),
			"platform:holding:"+strings.ToUpper(trade.FromCurrency),
			operationKey+":conversion_asset",
			metadata,
		); err != nil {
			return err
		}

		grossFiat := trade.ToAmountExpected + trade.FeeAmount
		if grossFiat > 0 {
			if err := s.postBalancedLedgerEntryTx(
				ctx,
				tx,
				trade.ID,
				"CONVERSION_FIAT",
				trade.ToCurrency,
				grossFiat,
				"platform:holding:"+strings.ToUpper(trade.ToCurrency),
				"exchange:inventory:"+strings.ToUpper(trade.ToCurrency),
				operationKey+":conversion_fiat",
				metadata,
			); err != nil {
				return err
			}
		}

		platformFee := trade.FeeAmount
		if platformFee > 0 {
			if err := s.postBalancedLedgerEntryTx(
				ctx,
				tx,
				trade.ID,
				"PLATFORM_FEE",
				trade.ToCurrency,
				platformFee,
				"platform:revenue:fee:"+strings.ToUpper(trade.ToCurrency),
				"platform:holding:"+strings.ToUpper(trade.ToCurrency),
				operationKey+":platform_fee",
				metadata,
			); err != nil {
				return err
			}
		}

		fxSpread := metadataInt64(metadata, "fx_spread_amount")
		if fxSpread > 0 {
			if err := s.postBalancedLedgerEntryTx(
				ctx,
				tx,
				trade.ID,
				"FX_SPREAD",
				trade.ToCurrency,
				fxSpread,
				"platform:revenue:spread:"+strings.ToUpper(trade.ToCurrency),
				"platform:holding:"+strings.ToUpper(trade.ToCurrency),
				operationKey+":fx_spread",
				metadata,
			); err != nil {
				return err
			}
		}

		expressFee := metadataInt64(metadata, "express_payout_fee")
		if expressFee > 0 {
			if err := s.postBalancedLedgerEntryTx(
				ctx,
				tx,
				trade.ID,
				"EXPRESS_PAYOUT_FEE",
				trade.ToCurrency,
				expressFee,
				"platform:revenue:express_payout:"+strings.ToUpper(trade.ToCurrency),
				"platform:holding:"+strings.ToUpper(trade.ToCurrency),
				operationKey+":express_fee",
				metadata,
			); err != nil {
				return err
			}
		}
		return nil

	case string(statemachine.TradePayoutCompleted):
		payoutAmount := resolvePayoutAmount(trade, metadata)
		if payoutAmount <= 0 {
			return nil
		}
		return s.postBalancedLedgerEntryTx(
			ctx,
			tx,
			trade.ID,
			"PAYOUT",
			trade.ToCurrency,
			payoutAmount,
			"user:"+trade.UserID.String()+":wallet:"+strings.ToUpper(trade.ToCurrency),
			"platform:holding:"+strings.ToUpper(trade.ToCurrency),
			operationKey+":payout",
			metadata,
		)

	case string(statemachine.TradePayoutFailed):
		failedAmount := resolvePayoutAmount(trade, metadata)
		if failedAmount <= 0 {
			return nil
		}
		return s.postBalancedLedgerEntryTx(
			ctx,
			tx,
			trade.ID,
			"PAYOUT_FAILED_RESERVE",
			trade.ToCurrency,
			failedAmount,
			"platform:suspense:payout_failed:"+strings.ToUpper(trade.ToCurrency),
			"platform:holding:"+strings.ToUpper(trade.ToCurrency),
			operationKey+":payout_failed",
			metadata,
		)

	case string(statemachine.TradeDispute):
		disputeAmount := resolvePayoutAmount(trade, metadata)
		if disputeAmount <= 0 {
			disputeAmount = trade.ToAmountExpected
		}
		if disputeAmount <= 0 {
			return nil
		}
		return s.postBalancedLedgerEntryTx(
			ctx,
			tx,
			trade.ID,
			"DISPUTE_ADJUSTMENT",
			trade.ToCurrency,
			disputeAmount,
			"platform:suspense:dispute:"+strings.ToUpper(trade.ToCurrency),
			"platform:holding:"+strings.ToUpper(trade.ToCurrency),
			operationKey+":dispute",
			metadata,
		)
	}

	return nil
}
