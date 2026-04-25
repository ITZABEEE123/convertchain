package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/domain"
	"convert-chain/go-engine/internal/pricing"
	"convert-chain/go-engine/internal/statemachine"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *ApplicationService) CreateQuote(ctx context.Context, req dto.QuoteRequest) (*domain.Quote, error) {
	user, err := s.getUserByID(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if userStatusToAPI(user.Status) != "APPROVED" {
		return nil, fmt.Errorf("kyc_not_approved")
	}

	asset := strings.ToUpper(strings.TrimSpace(req.Asset))
	amountMinor, err := decimalStringToMinorUnits(req.Amount, assetDecimals(asset))
	if err != nil {
		return nil, err
	}
	if amountMinor <= 0 {
		return nil, fmt.Errorf("amount must be greater than zero")
	}

	priceQuote, err := s.generateQuote(ctx, req.UserID, asset, amountMinor)
	if err != nil {
		return nil, err
	}

	if err := s.enforceQuoteTierLimits(ctx, user, priceQuote.ToAmount); err != nil {
		return nil, err
	}
	if err := s.evaluateScreeningChecks(ctx, user, nil, nil, nil, asset); err != nil {
		return nil, err
	}
	if err := s.evaluateQuoteMonitoring(ctx, user, priceQuote.ToAmount, nil); err != nil {
		return nil, err
	}

	quote := &domain.Quote{}
	err = scanQuote(
		s.db.QueryRow(ctx, `
            INSERT INTO quotes (
                user_id,
                from_currency,
                to_currency,
                from_amount,
                to_amount,
                exchange_rate,
                fiat_rate,
                fee_bps,
                fee_amount,
                net_amount,
                valid_until
            ) VALUES (
                $1::uuid,
                $2::currency_code,
                'NGN'::currency_code,
                $3,
                $4,
                $5::numeric,
                $6::numeric,
                $7,
                $8,
                $9,
                $10
            )
            RETURNING
                id,
                user_id,
                from_currency::text,
                to_currency::text,
                from_amount,
                to_amount,
                net_amount,
                exchange_rate::text,
                fiat_rate::text,
                fee_bps,
                fee_amount,
                valid_until,
                accepted_at,
                expired_at,
                created_at
        `,
			req.UserID,
			asset,
			priceQuote.FromAmount,
			priceQuote.GrossAmount,
			priceQuote.ExchangeRate,
			priceQuote.FiatRate,
			priceQuote.FeeBPS,
			priceQuote.FeeAmount,
			priceQuote.ToAmount,
			priceQuote.ValidUntil.UTC(),
		),
		quote,
	)
	if err != nil {
		return nil, err
	}

	quote.PricingMode = priceQuote.PricingMode
	quote.PriceSource = priceQuote.PriceSource
	quote.FiatRateSource = priceQuote.FiatRateSource
	quote.MarketRatePerUnitKobo = priceQuote.MarketRatePerUnitKobo
	quote.UserRatePerUnitKobo = priceQuote.UserRatePerUnitKobo

	return quote, nil
}

func (s *ApplicationService) CreateTrade(ctx context.Context, req dto.CreateTradeRequest) (*domain.Trade, error) {
	authorizedAt := time.Now().UTC()
	return s.createTradeWithAuthorization(
		ctx,
		req.UserID,
		req.QuoteID,
		req.BankAccountID,
		&authorizedAt,
		"legacy-service",
	)
}

func (s *ApplicationService) ConfirmTrade(ctx context.Context, req dto.ConfirmTradeRequest) (*domain.Trade, error) {
	user, err := s.getUserByID(ctx, req.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errors.New("user_not_found")
		}
		return nil, err
	}

	if err := s.verifyTransactionPassword(ctx, buildPasswordState(user), req.TransactionPassword); err != nil {
		return nil, err
	}

	authorizedAt := time.Now().UTC()
	return s.createTradeWithAuthorization(
		ctx,
		req.UserID,
		req.QuoteID,
		req.BankAccountID,
		&authorizedAt,
		"transaction_password",
	)
}

func (s *ApplicationService) createTradeWithAuthorization(
	ctx context.Context,
	userID string,
	quoteID string,
	bankAccountID string,
	authorizedAt *time.Time,
	authorizationMethod string,
) (*domain.Trade, error) {
	user, err := s.getUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errors.New("user_not_found")
		}
		return nil, err
	}

	quote, err := s.getQuoteByID(ctx, quoteID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errors.New("quote_not_found")
		}
		return nil, err
	}

	if quote.AcceptedAt != nil {
		return nil, errors.New("quote_already_used")
	}
	if quote.ExpiredAt != nil || time.Now().UTC().After(quote.ValidUntil) {
		return nil, errors.New("quote_expired")
	}
	if quote.UserID.String() != userID {
		return nil, errors.New("quote_not_found")
	}

	account, err := s.getBankAccountByID(ctx, bankAccountID)
	if err != nil {
		return nil, err
	}
	if account.UserID != quote.UserID {
		return nil, errors.New("bank_account_not_found")
	}
	if err := s.preflightTradeConfirmation(ctx, quote, account); err != nil {
		return nil, err
	}

	if err := s.enforceTradeTierLimits(ctx, user, quote.NetAmount); err != nil {
		return nil, err
	}
	if err := s.evaluateScreeningChecks(ctx, user, account, &quote.ID, nil, quote.FromCurrency); err != nil {
		return nil, err
	}

	tradeID := uuid.New()
	tradeRef := "TRD-" + strings.ToUpper(strings.ReplaceAll(tradeID.String(), "-", "")[:8])
	depositAddress, err := buildDepositAddressForTrade(quote.FromCurrency, tradeRef)
	if err != nil {
		return nil, err
	}

	if err := s.evaluateTradeMonitoring(ctx, user, account, quote, &tradeID, depositAddress); err != nil {
		return nil, err
	}
	expiresAt := time.Now().UTC().Add(30 * time.Minute)
	acceptedAt := time.Now().UTC()

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	tradeCreateKey := normalizeMoneyOperationKey(fmt.Sprintf("trade_create:%s:%s:%s", userID, quoteID, bankAccountID))
	inserted, err := s.reserveMoneyOperationKeyTx(ctx, tx, "trade_creation", tradeCreateKey, uuid.Nil)
	if err != nil {
		return nil, err
	}
	if !inserted {
		return nil, errors.New("duplicate_trade_request")
	}

	_, err = tx.Exec(ctx, `UPDATE quotes SET accepted_at = $2 WHERE id = $1::uuid`, quoteID, acceptedAt)
	if err != nil {
		return nil, err
	}

	trade := &domain.Trade{}
	err = scanTrade(
		tx.QueryRow(ctx, `
            INSERT INTO trades (
                id,
                trade_ref,
                user_id,
                quote_id,
                status,
                from_currency,
                to_currency,
                from_amount,
                to_amount_expected,
                fee_amount,
                deposit_address,
                bank_account_id,
                expires_at,
                payout_authorized_at,
                payout_authorization_method
            ) VALUES (
                $1::uuid,
                $2,
                $3::uuid,
                $4::uuid,
                'PENDING_DEPOSIT'::trade_status,
                $5::currency_code,
                $6::currency_code,
                $7,
                $8,
                $9,
                $10,
                $11::uuid,
                $12,
                $13,
                NULLIF($14, '')
            )
            RETURNING
                id,
                trade_ref,
                user_id,
                quote_id,
                bank_account_id,
                status::text,
                from_currency::text,
                to_currency::text,
                from_amount,
                to_amount_expected,
                to_amount_actual,
                fee_amount,
                deposit_address,
                deposit_txhash,
                deposit_confirmed_at,
                exchange_order_id,
                graph_conversion_id,
                graph_payout_id,
                payout_authorized_at,
                payout_authorization_method,
                dispute_reason,
                expires_at,
                completed_at,
                created_at,
                updated_at
        `,
			tradeID,
			tradeRef,
			userID,
			quoteID,
			quote.FromCurrency,
			quote.ToCurrency,
			quote.FromAmount,
			quote.NetAmount,
			quote.FeeAmount,
			depositAddress,
			bankAccountID,
			expiresAt,
			authorizedAt,
			authorizationMethod,
		),
		trade,
	)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx, `
        UPDATE financial_operation_keys
        SET trade_id = $3::uuid
        WHERE scope = $1
          AND operation_key = $2
    `, "trade_creation", tradeCreateKey, trade.ID)
	if err != nil {
		return nil, err
	}

	if err := insertTradeHistory(ctx, tx, trade.ID, nil, trade.Status, "user", "quote accepted"); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return trade, nil
}

func (s *ApplicationService) GetTrade(ctx context.Context, tradeID string) (*domain.Trade, error) {
	return s.getTradeByID(ctx, tradeID)
}

func (s *ApplicationService) GetLatestActiveTradeForUser(ctx context.Context, userID string) (*domain.Trade, error) {
	parsedUserID, err := uuid.Parse(strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	return s.getFirstBlockingActiveTrade(ctx, parsedUserID)
}

func (s *ApplicationService) GetTradesByStatus(ctx context.Context, status string) ([]*domain.Trade, error) {
	rows, err := s.db.Query(ctx, `
        SELECT
            id,
            trade_ref,
            user_id,
            quote_id,
            bank_account_id,
            status::text,
            from_currency::text,
            to_currency::text,
            from_amount,
            to_amount_expected,
            to_amount_actual,
            fee_amount,
            deposit_address,
            deposit_txhash,
            deposit_confirmed_at,
            exchange_order_id,
            graph_conversion_id,
            graph_payout_id,
            payout_authorized_at,
            payout_authorization_method,
            dispute_reason,
            expires_at,
            completed_at,
            created_at,
            updated_at
        FROM trades
        WHERE status = $1::trade_status
        ORDER BY created_at ASC
    `, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	trades := make([]*domain.Trade, 0)
	for rows.Next() {
		trade := &domain.Trade{}
		if err := scanTrade(rows, trade); err != nil {
			return nil, err
		}
		trades = append(trades, trade)
	}
	return trades, rows.Err()
}

func (s *ApplicationService) GetTradeByID(ctx context.Context, tradeID string) (*domain.Trade, error) {
	return s.getTradeByID(ctx, tradeID)
}

func (s *ApplicationService) GetTradeByDepositTxHash(ctx context.Context, txHash string) (*domain.Trade, error) {
	return s.getTradeByDepositTxHash(ctx, txHash)
}

func (s *ApplicationService) UpdateTradeStatus(ctx context.Context, tradeID string, status string, metadata map[string]interface{}) error {
	trade, err := s.getTradeByID(ctx, tradeID)
	if err != nil {
		return err
	}
	if trade == nil {
		return pgx.ErrNoRows
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := s.updateTradeStatusTx(ctx, tx, trade, status, metadata, "system"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *ApplicationService) GetPendingPayouts(ctx context.Context) ([]*domain.Trade, error) {
	rows, err := s.db.Query(ctx, `
        SELECT
            id,
            trade_ref,
            user_id,
            quote_id,
            bank_account_id,
            status::text,
            from_currency::text,
            to_currency::text,
            from_amount,
            to_amount_expected,
            to_amount_actual,
            fee_amount,
            deposit_address,
            deposit_txhash,
            deposit_confirmed_at,
            exchange_order_id,
            graph_conversion_id,
            graph_payout_id,
            payout_authorized_at,
            payout_authorization_method,
            dispute_reason,
            expires_at,
            completed_at,
            created_at,
            updated_at
        FROM trades
        WHERE status = $1::trade_status
          AND payout_authorized_at IS NOT NULL
          AND graph_payout_id IS NULL
        ORDER BY created_at ASC
    `, string(statemachine.TradeConversionCompleted))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	trades := make([]*domain.Trade, 0)
	for rows.Next() {
		trade := &domain.Trade{}
		if err := scanTrade(rows, trade); err != nil {
			return nil, err
		}
		trades = append(trades, trade)
	}
	return trades, rows.Err()
}

func (s *ApplicationService) MarkPayoutPending(ctx context.Context, tradeID string, payoutRef string) error {
	return s.UpdateTradeStatus(ctx, tradeID, string(statemachine.TradePayoutPending), map[string]interface{}{
		"payout_ref":      payoutRef,
		"reason":          "graph payout initiated",
		"idempotency_key": fmt.Sprintf("payout_pending:%s:%s", tradeID, strings.ToLower(strings.TrimSpace(payoutRef))),
	})
}

func (s *ApplicationService) MarkPayoutComplete(ctx context.Context, tradeID string, payoutRef string) error {
	return s.UpdateTradeStatus(ctx, tradeID, string(statemachine.TradePayoutCompleted), map[string]interface{}{
		"payout_ref":      payoutRef,
		"idempotency_key": fmt.Sprintf("payout_completed:%s:%s", tradeID, strings.ToLower(strings.TrimSpace(payoutRef))),
	})
}

func (s *ApplicationService) MarkPayoutFailed(ctx context.Context, tradeID string, payoutRef string, reason string) error {
	return s.UpdateTradeStatus(ctx, tradeID, string(statemachine.TradePayoutFailed), map[string]interface{}{
		"payout_ref":      payoutRef,
		"reason":          reason,
		"idempotency_key": fmt.Sprintf("payout_failed:%s:%s", tradeID, strings.ToLower(strings.TrimSpace(payoutRef))),
	})
}

func (s *ApplicationService) GetExpiredPendingQuotes(ctx context.Context, now time.Time) ([]*domain.Quote, error) {
	rows, err := s.db.Query(ctx, `
        SELECT
            id,
            user_id,
            from_currency::text,
            to_currency::text,
            from_amount,
            to_amount,
            net_amount,
            exchange_rate::text,
            fiat_rate::text,
            fee_bps,
            fee_amount,
            valid_until,
            accepted_at,
            expired_at,
            created_at
        FROM quotes
        WHERE accepted_at IS NULL
          AND expired_at IS NULL
          AND valid_until <= $1
        ORDER BY valid_until ASC
    `, now.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	quotes := make([]*domain.Quote, 0)
	for rows.Next() {
		quote := &domain.Quote{}
		if err := scanQuote(rows, quote); err != nil {
			return nil, err
		}
		quotes = append(quotes, quote)
	}
	return quotes, rows.Err()
}

func (s *ApplicationService) ExpireQuote(ctx context.Context, quoteID string) error {
	_, err := s.db.Exec(ctx, `
        UPDATE quotes
        SET expired_at = NOW()
        WHERE id = $1::uuid
          AND accepted_at IS NULL
          AND expired_at IS NULL
    `, quoteID)
	return err
}

func (s *ApplicationService) generateQuote(ctx context.Context, userID, asset string, fromAmount int64) (*pricing.QuoteResponse, error) {
	if s.pricingEngine != nil {
		response, err := s.pricingEngine.GetQuote(ctx, pricing.QuoteRequest{
			UserID:       userID,
			FromCurrency: asset,
			ToCurrency:   "NGN",
			FromAmount:   fromAmount,
		})
		if err == nil {
			return response, nil
		}
		s.logger.Warn("pricing engine failed; using local fallback", "asset", asset, "error", err)
	}

	return s.generateFallbackQuote(asset, fromAmount)
}

func (s *ApplicationService) generateFallbackQuote(asset string, fromAmount int64) (*pricing.QuoteResponse, error) {
	wholeAmount, err := minorUnitsToFloat(fromAmount, assetDecimals(asset))
	if err != nil {
		return nil, err
	}

	cryptoPrice := fallbackCryptoPrice(asset)
	fiatRate := 1650.0
	grossNGN := wholeAmount * cryptoPrice * fiatRate
	grossKobo := int64(grossNGN * 100)
	feeBPS := 200
	feeKobo := grossKobo * int64(feeBPS) / 10000
	netKobo := grossKobo - feeKobo

	return &pricing.QuoteResponse{
		QuoteID:               uuid.NewString(),
		FromCurrency:          asset,
		ToCurrency:            "NGN",
		FromAmount:            fromAmount,
		GrossAmount:           grossKobo,
		FeeAmount:             feeKobo,
		ToAmount:              netKobo,
		FeeBPS:                feeBPS,
		ExchangeRate:          fmt.Sprintf("%.8f", cryptoPrice),
		FiatRate:              fmt.Sprintf("%.2f", fiatRate),
		ValidUntil:            time.Now().UTC().Add(pricing.QuoteTTL),
		PricingMode:           "sandbox_fallback",
		PriceSource:           "fallback",
		FiatRateSource:        "fallback",
		MarketRatePerUnitKobo: pricingRatePerUnitKobo(grossKobo, fromAmount, asset),
		UserRatePerUnitKobo:   pricingRatePerUnitKobo(netKobo, fromAmount, asset),
	}, nil
}

func pricingRatePerUnitKobo(totalKobo int64, fromAmount int64, asset string) int64 {
	amountFloat, err := minorUnitsToFloat(fromAmount, assetDecimals(asset))
	if err != nil || amountFloat <= 0 {
		return 0
	}
	return int64(float64(totalKobo) / amountFloat)
}

func (s *ApplicationService) updateTradeStatusTx(
	ctx context.Context,
	tx pgx.Tx,
	trade *domain.Trade,
	status string,
	metadata map[string]interface{},
	actor string,
) error {
	if trade == nil {
		return pgx.ErrNoRows
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}

	if !isAllowedTradeStatusTransition(trade.Status, status) {
		return fmt.Errorf("invalid trade status transition: %s -> %s", trade.Status, status)
	}

	operationKey := buildTradeOperationKey(trade, status, metadata)
	inserted, err := s.reserveMoneyOperationKeyTx(ctx, tx, statusLedgerScope(status), operationKey, trade.ID)
	if err != nil {
		return err
	}
	if !inserted {
		s.logger.Info("duplicate trade status update ignored", "trade_id", trade.ID.String(), "status", status, "operation_key", operationKey)
		return nil
	}

	var txHash *string
	var confirmedAt *time.Time
	var payoutRef *string
	var exchangeOrderID *string
	var graphConversionID *string
	var toAmountActual *int64
	var completedAt *time.Time
	var disputeReason *string
	clearDisputeReason := false
	note := strings.TrimSpace(stringValueFromMap(metadata, "reason"))

	if raw, ok := metadata["tx_hash"].(string); ok && strings.TrimSpace(raw) != "" {
		trimmed := strings.TrimSpace(raw)
		txHash = &trimmed
	}
	if raw, ok := metadata["payout_ref"].(string); ok && strings.TrimSpace(raw) != "" {
		trimmed := strings.TrimSpace(raw)
		payoutRef = &trimmed
	}
	if raw, ok := metadata["exchange_order_id"].(string); ok && strings.TrimSpace(raw) != "" {
		trimmed := strings.TrimSpace(raw)
		exchangeOrderID = &trimmed
	}
	if raw, ok := metadata["graph_conversion_id"].(string); ok && strings.TrimSpace(raw) != "" {
		trimmed := strings.TrimSpace(raw)
		graphConversionID = &trimmed
	}
	switch value := metadata["to_amount_actual"].(type) {
	case int64:
		copyValue := value
		toAmountActual = &copyValue
	case int:
		copyValue := int64(value)
		toAmountActual = &copyValue
	case float64:
		copyValue := int64(value)
		toAmountActual = &copyValue
	}
	if raw, ok := metadata["clear_dispute_reason"].(bool); ok && raw {
		clearDisputeReason = true
	}

	if note != "" && (status == string(statemachine.TradeDispute) || status == string(statemachine.TradePayoutFailed) || status == string(statemachine.TradeDisputeClosed)) {
		disputeReason = &note
	}

	if status == string(statemachine.TradeDepositConfirmed) {
		now := time.Now().UTC()
		confirmedAt = &now
	}
	if status == string(statemachine.TradePayoutCompleted) {
		now := time.Now().UTC()
		completedAt = &now
	}

	_, err = tx.Exec(ctx, `
        UPDATE trades
        SET status = $2::trade_status,
            deposit_txhash = COALESCE($3, deposit_txhash),
            deposit_confirmed_at = COALESCE($4, deposit_confirmed_at),
            graph_payout_id = COALESCE($5, graph_payout_id),
            completed_at = COALESCE($6, completed_at),
            exchange_order_id = COALESCE($7, exchange_order_id),
            graph_conversion_id = COALESCE($8, graph_conversion_id),
            to_amount_actual = COALESCE($9, to_amount_actual),
            dispute_reason = CASE
				WHEN $11 THEN NULL::text
				WHEN $10::text IS NULL THEN dispute_reason
				ELSE $10::text
            END
        WHERE id = $1::uuid
    `, trade.ID, status, txHash, confirmedAt, payoutRef, completedAt, exchangeOrderID, graphConversionID, toAmountActual, disputeReason, clearDisputeReason)
	if err != nil {
		return err
	}

	skipLedgerPosting := false
	if raw, ok := metadata["skip_ledger_posting"].(bool); ok && raw {
		skipLedgerPosting = true
	}
	if !skipLedgerPosting {
		if err := s.postLedgerForStatusTransitionTx(ctx, tx, trade, status, metadata, operationKey); err != nil {
			return err
		}
	}

	if err := insertTradeHistory(ctx, tx, trade.ID, &trade.Status, status, actor, note); err != nil {
		return err
	}

	trade.Status = status
	if txHash != nil {
		trade.DepositTxHash = txHash
	}
	if confirmedAt != nil {
		trade.DepositConfirmedAt = confirmedAt
	}
	if payoutRef != nil {
		trade.GraphPayoutID = payoutRef
	}
	if completedAt != nil {
		trade.CompletedAt = completedAt
	}
	if exchangeOrderID != nil {
		trade.ExchangeOrderID = exchangeOrderID
	}
	if graphConversionID != nil {
		trade.GraphConversionID = graphConversionID
	}
	if toAmountActual != nil {
		trade.ToAmountActual = toAmountActual
	}
	if clearDisputeReason {
		trade.DisputeReason = nil
	} else if disputeReason != nil {
		trade.DisputeReason = disputeReason
	}

	if err := s.enqueueTradeNotificationTx(ctx, tx, trade, status, metadata); err != nil {
		return err
	}

	if status == string(statemachine.TradeDispute) {
		disputeSource := strings.TrimSpace(stringValueFromMap(metadata, "dispute_source"))
		dispute, err := s.openTradeDisputeTx(ctx, tx, trade, disputeSource, note)
		if err != nil {
			return err
		}
		if dispute != nil {
			if err := s.enqueueTradeDisputeNotificationTx(ctx, tx, trade, dispute, "trade.dispute_opened"); err != nil {
				return err
			}
		}
	}

	return nil
}
