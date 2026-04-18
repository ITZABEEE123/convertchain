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

    return quote, nil
}

func (s *ApplicationService) CreateTrade(ctx context.Context, req dto.CreateTradeRequest) (*domain.Trade, error) {
    quote, err := s.getQuoteByID(ctx, req.QuoteID)
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

    account, err := s.getBankAccountByID(ctx, req.BankAccountID)
    if err != nil {
        return nil, err
    }
    if account.UserID != quote.UserID {
        return nil, errors.New("bank_account_not_found")
    }

    tradeID := uuid.New()
    tradeRef := "TRD-" + strings.ToUpper(strings.ReplaceAll(tradeID.String(), "-", "")[:8])
    depositAddress := fmt.Sprintf("sandbox://deposit/%s/%s", strings.ToLower(quote.FromCurrency), strings.ToLower(tradeRef))
    expiresAt := time.Now().UTC().Add(30 * time.Minute)
    acceptedAt := time.Now().UTC()

    tx, err := s.db.Begin(ctx)
    if err != nil {
        return nil, err
    }
    defer tx.Rollback(ctx)

    _, err = tx.Exec(ctx, `UPDATE quotes SET accepted_at = $2 WHERE id = $1::uuid`, req.QuoteID, acceptedAt)
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
                expires_at
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
                $12
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
                dispute_reason,
                expires_at,
                completed_at,
                created_at,
                updated_at
        `,
            tradeID,
            tradeRef,
            req.UserID,
            req.QuoteID,
            quote.FromCurrency,
            quote.ToCurrency,
            quote.FromAmount,
            quote.NetAmount,
            quote.FeeAmount,
            depositAddress,
            req.BankAccountID,
            expiresAt,
        ),
        trade,
    )
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

    var txHash *string
    var confirmedAt *time.Time
    var payoutRef *string
    var completedAt *time.Time
    note := ""

    if raw, ok := metadata["tx_hash"].(string); ok && raw != "" {
        txHash = &raw
    }
    if raw, ok := metadata["payout_ref"].(string); ok && raw != "" {
        payoutRef = &raw
    }
    if raw, ok := metadata["reason"].(string); ok && raw != "" {
        note = raw
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
            completed_at = COALESCE($6, completed_at)
        WHERE id = $1::uuid
    `, tradeID, status, txHash, confirmedAt, payoutRef, completedAt)
    if err != nil {
        return err
    }

    if err := insertTradeHistory(ctx, tx, trade.ID, &trade.Status, status, "system", note); err != nil {
        return err
    }

    return tx.Commit(ctx)
}

func (s *ApplicationService) GetPendingPayouts(ctx context.Context) ([]*domain.Trade, error) {
    return s.GetTradesByStatus(ctx, string(statemachine.TradeDepositConfirmed))
}

func (s *ApplicationService) MarkPayoutComplete(ctx context.Context, tradeID string, payoutRef string) error {
    return s.UpdateTradeStatus(ctx, tradeID, string(statemachine.TradePayoutCompleted), map[string]interface{}{
        "payout_ref": payoutRef,
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
        QuoteID:      uuid.NewString(),
        FromCurrency: asset,
        ToCurrency:   "NGN",
        FromAmount:   fromAmount,
        GrossAmount:  grossKobo,
        FeeAmount:    feeKobo,
        ToAmount:     netKobo,
        FeeBPS:       feeBPS,
        ExchangeRate: fmt.Sprintf("%.8f", cryptoPrice),
        FiatRate:     fmt.Sprintf("%.2f", fiatRate),
        ValidUntil:   time.Now().UTC().Add(pricing.QuoteTTL),
    }, nil
}
