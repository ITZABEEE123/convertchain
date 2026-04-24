package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"convert-chain/go-engine/internal/domain"
	"convert-chain/go-engine/internal/statemachine"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	disputeStatusOpen   = "OPEN"
	disputeStatusClosed = "CLOSED"

	disputeSourceSystem = "system"
	disputeSourceUser   = "user"

	DisputeResolutionRetryProcessing = "retry_processing"
	DisputeResolutionCloseNoPayout   = "close_no_payout"
	DisputeResolutionForceComplete   = "force_complete"
)

type TradePreflightError struct {
	Message string
	Details map[string]interface{}
}

func (e *TradePreflightError) Error() string {
	return e.Message
}

func (e *TradePreflightError) DetailsMap() map[string]interface{} {
	return e.Details
}

type NotificationAckConflictError struct {
	NotificationID string
	Message        string
}

func (e *NotificationAckConflictError) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return "notification acknowledgement could not be applied"
}

func (e *NotificationAckConflictError) Conflict() bool {
	return true
}

func scanTradeDispute(src rowScanner, dispute *domain.TradeDispute) error {
	return src.Scan(
		&dispute.ID,
		&dispute.TicketRef,
		&dispute.TradeID,
		&dispute.TradeRef,
		&dispute.UserID,
		&dispute.Source,
		&dispute.Status,
		&dispute.Reason,
		&dispute.ResolutionMode,
		&dispute.ResolutionNote,
		&dispute.Resolver,
		&dispute.CreatedAt,
		&dispute.ResolvedAt,
		&dispute.UpdatedAt,
	)
}

func (s *ApplicationService) getTradeDisputeByIdentifier(ctx context.Context, identifier string) (*domain.TradeDispute, error) {
	trimmed := strings.TrimSpace(identifier)
	if trimmed == "" {
		return nil, nil
	}

	query := `
        SELECT
            d.id,
            d.ticket_ref,
            d.trade_id,
            t.trade_ref,
            t.user_id,
            d.source,
            d.status,
            d.reason,
            d.resolution_mode,
            d.resolution_note,
            d.resolver,
            d.created_at,
            d.resolved_at,
            d.updated_at
        FROM trade_disputes d
        JOIN trades t ON t.id = d.trade_id
        WHERE %s
        ORDER BY
            CASE WHEN d.status = 'OPEN' THEN 0 ELSE 1 END,
            d.created_at DESC
        LIMIT 1
    `

	dispute := &domain.TradeDispute{}
	if parsedID, err := uuid.Parse(trimmed); err == nil {
		err = scanTradeDispute(
			s.db.QueryRow(ctx, fmt.Sprintf(query, "(d.id = $1::uuid OR d.trade_id = $1::uuid)"), parsedID),
			dispute,
		)
		if err == nil {
			return dispute, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}

	err := scanTradeDispute(
		s.db.QueryRow(ctx, fmt.Sprintf(query, "(UPPER(d.ticket_ref) = UPPER($1) OR UPPER(t.trade_ref) = UPPER($1))"), trimmed),
		dispute,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return dispute, nil
}

func getOpenTradeDisputeTx(ctx context.Context, tx pgx.Tx, tradeID uuid.UUID) (*domain.TradeDispute, error) {
	dispute := &domain.TradeDispute{}
	err := scanTradeDispute(
		tx.QueryRow(ctx, `
            SELECT
                d.id,
                d.ticket_ref,
                d.trade_id,
                t.trade_ref,
                t.user_id,
                d.source,
                d.status,
                d.reason,
                d.resolution_mode,
                d.resolution_note,
                d.resolver,
                d.created_at,
                d.resolved_at,
                d.updated_at
            FROM trade_disputes d
            JOIN trades t ON t.id = d.trade_id
            WHERE d.trade_id = $1::uuid
              AND d.status = $2
            ORDER BY d.created_at DESC
            LIMIT 1
        `, tradeID, disputeStatusOpen),
		dispute,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return dispute, nil
}

func (s *ApplicationService) getLatestTradeDisputeForTrade(ctx context.Context, tradeID uuid.UUID) (*domain.TradeDispute, error) {
	dispute := &domain.TradeDispute{}
	err := scanTradeDispute(
		s.db.QueryRow(ctx, `
            SELECT
                d.id,
                d.ticket_ref,
                d.trade_id,
                t.trade_ref,
                t.user_id,
                d.source,
                d.status,
                d.reason,
                d.resolution_mode,
                d.resolution_note,
                d.resolver,
                d.created_at,
                d.resolved_at,
                d.updated_at
            FROM trade_disputes d
            JOIN trades t ON t.id = d.trade_id
            WHERE d.trade_id = $1::uuid
            ORDER BY d.created_at DESC
            LIMIT 1
        `, tradeID),
		dispute,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return dispute, nil
}

func (s *ApplicationService) ListTradeDisputes(ctx context.Context, status string, limit int) ([]*domain.TradeDispute, error) {
	normalizedStatus := strings.ToUpper(strings.TrimSpace(status))
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	args := []interface{}{limit}
	filter := ""
	if normalizedStatus == disputeStatusOpen || normalizedStatus == disputeStatusClosed {
		filter = "WHERE d.status = $2"
		args = append(args, normalizedStatus)
	}

	rows, err := s.db.Query(ctx, fmt.Sprintf(`
        SELECT
            d.id,
            d.ticket_ref,
            d.trade_id,
            t.trade_ref,
            t.user_id,
            d.source,
            d.status,
            d.reason,
            d.resolution_mode,
            d.resolution_note,
            d.resolver,
            d.created_at,
            d.resolved_at,
            d.updated_at
        FROM trade_disputes d
        JOIN trades t ON t.id = d.trade_id
        %s
        ORDER BY
            CASE WHEN d.status = 'OPEN' THEN 0 ELSE 1 END,
            d.created_at DESC
        LIMIT $1
    `, filter), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	disputes := make([]*domain.TradeDispute, 0)
	for rows.Next() {
		dispute := &domain.TradeDispute{}
		if err := scanTradeDispute(rows, dispute); err != nil {
			return nil, err
		}
		disputes = append(disputes, dispute)
	}
	return disputes, rows.Err()
}

func (s *ApplicationService) GetTradeDispute(ctx context.Context, identifier string) (*domain.TradeDispute, error) {
	return s.getTradeDisputeByIdentifier(ctx, identifier)
}

func (s *ApplicationService) openTradeDisputeTx(
	ctx context.Context,
	tx pgx.Tx,
	trade *domain.Trade,
	source string,
	reason string,
) (*domain.TradeDispute, error) {
	if trade == nil {
		return nil, pgx.ErrNoRows
	}

	existing, err := getOpenTradeDisputeTx(ctx, tx, trade.ID)
	if err != nil || existing != nil {
		return existing, err
	}

	normalizedSource := strings.ToLower(strings.TrimSpace(source))
	if normalizedSource != disputeSourceUser {
		normalizedSource = disputeSourceSystem
	}
	normalizedReason := strings.TrimSpace(reason)
	if normalizedReason == "" {
		normalizedReason = "Trade requires manual review"
	}

	disputeID := uuid.New()
	ticketRef := "DSP-" + strings.ToUpper(strings.ReplaceAll(disputeID.String(), "-", "")[:8])

	dispute := &domain.TradeDispute{}
	err = scanTradeDispute(
		tx.QueryRow(ctx, `
            INSERT INTO trade_disputes (
                id,
                ticket_ref,
                trade_id,
                source,
                status,
                reason
            ) VALUES (
                $1::uuid,
                $2,
                $3::uuid,
                $4,
                $5,
                $6
            )
            RETURNING
                id,
                ticket_ref,
                trade_id,
                $7,
                $8::uuid,
                source,
                status,
                reason,
                resolution_mode,
                resolution_note,
                resolver,
                created_at,
                resolved_at,
                updated_at
        `, disputeID, ticketRef, trade.ID, normalizedSource, disputeStatusOpen, normalizedReason, trade.TradeRef, trade.UserID),
		dispute,
	)
	if err != nil {
		return nil, err
	}
	return dispute, nil
}

func closeTradeDisputeTx(
	ctx context.Context,
	tx pgx.Tx,
	dispute *domain.TradeDispute,
	mode string,
	note string,
	resolver string,
) (*domain.TradeDispute, error) {
	if dispute == nil {
		return nil, pgx.ErrNoRows
	}

	normalizedMode := strings.TrimSpace(mode)
	normalizedNote := strings.TrimSpace(note)
	normalizedResolver := strings.TrimSpace(resolver)
	if normalizedResolver == "" {
		normalizedResolver = "admin"
	}

	updated := &domain.TradeDispute{}
	err := scanTradeDispute(
		tx.QueryRow(ctx, `
            UPDATE trade_disputes
            SET status = $2,
                resolution_mode = $3,
				resolution_note = NULLIF($4::text, ''),
                resolver = $5,
                resolved_at = NOW()
            WHERE id = $1::uuid
            RETURNING
                id,
                ticket_ref,
                trade_id,
                $6,
                $7::uuid,
                source,
                status,
                reason,
                resolution_mode,
                resolution_note,
                resolver,
                created_at,
                resolved_at,
                updated_at
        `, dispute.ID, disputeStatusClosed, normalizedMode, normalizedNote, normalizedResolver, dispute.TradeRef, dispute.UserID),
		updated,
	)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *ApplicationService) enqueueTradeDisputeNotificationTx(
	ctx context.Context,
	tx pgx.Tx,
	trade *domain.Trade,
	dispute *domain.TradeDispute,
	eventType string,
) error {
	if trade == nil || dispute == nil || strings.TrimSpace(eventType) == "" {
		return nil
	}

	payload := map[string]interface{}{
		"trade_id":        trade.ID.String(),
		"trade_ref":       trade.TradeRef,
		"status":          trade.Status,
		"ticket_ref":      dispute.TicketRef,
		"dispute_id":      dispute.ID.String(),
		"source":          dispute.Source,
		"reason":          dispute.Reason,
		"resolution_mode": stringValuePointer(dispute.ResolutionMode),
		"resolution_note": stringValuePointer(dispute.ResolutionNote),
	}
	if trade.BankAccID != nil {
		bankName, maskedNumber, err := loadTradeBankSummaryTx(ctx, tx, *trade.BankAccID)
		if err != nil {
			return err
		}
		payload["bank_name"] = bankName
		payload["masked_account_number"] = maskedNumber
	}
	if dispute.ResolvedAt != nil {
		payload["resolved_at"] = dispute.ResolvedAt.UTC().Format(time.RFC3339)
	}

	return s.enqueueNotificationTx(
		ctx,
		tx,
		trade.UserID,
		&trade.ID,
		eventType,
		payload,
		fmt.Sprintf("%s:%s:%s", dispute.ID.String(), eventType, trade.Status),
	)
}

func (s *ApplicationService) ResolveTradeDispute(
	ctx context.Context,
	identifier string,
	mode string,
	note string,
	resolver string,
) (*domain.TradeDispute, *domain.Trade, error) {
	dispute, err := s.getTradeDisputeByIdentifier(ctx, identifier)
	if err != nil {
		return nil, nil, err
	}
	if dispute == nil {
		return nil, nil, pgx.ErrNoRows
	}
	if dispute.Status != disputeStatusOpen {
		return nil, nil, fmt.Errorf("dispute_already_closed")
	}

	trade, err := s.getTradeByID(ctx, dispute.TradeID.String())
	if err != nil {
		return nil, nil, err
	}
	if trade == nil {
		return nil, nil, pgx.ErrNoRows
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	resolutionMode := strings.TrimSpace(mode)
	resolutionNote := strings.TrimSpace(note)
	switch resolutionMode {
	case DisputeResolutionRetryProcessing:
		nextStatus := retryStatusForTrade(trade)
		if nextStatus == string(statemachine.TradeConversionCompleted) {
			if _, err := tx.Exec(ctx, `
                UPDATE trades
                SET graph_payout_id = NULL,
                    completed_at = NULL
                WHERE id = $1::uuid
            `, trade.ID); err != nil {
				return nil, nil, err
			}
			trade.GraphPayoutID = nil
			trade.CompletedAt = nil
		}
		if err := s.updateTradeStatusTx(ctx, tx, trade, nextStatus, map[string]interface{}{
			"reason":               firstNonEmptyValue(resolutionNote, dispute.Reason),
			"clear_dispute_reason": true,
		}, "admin"); err != nil {
			return nil, nil, err
		}
	case DisputeResolutionCloseNoPayout:
		closeReason := resolutionNote
		if closeReason == "" {
			closeReason = "Closed without payout by admin review"
		}
		if err := s.updateTradeStatusTx(ctx, tx, trade, string(statemachine.TradeDisputeClosed), map[string]interface{}{
			"reason": closeReason,
		}, "admin"); err != nil {
			return nil, nil, err
		}
	case DisputeResolutionForceComplete:
		payoutRef := strings.TrimSpace(stringValuePointer(trade.GraphPayoutID))
		if payoutRef == "" {
			payoutRef = "ADMIN-FORCED-" + strings.ToUpper(strings.ReplaceAll(trade.ID.String(), "-", "")[:8])
		}
		if err := s.updateTradeStatusTx(ctx, tx, trade, string(statemachine.TradePayoutCompleted), map[string]interface{}{
			"payout_ref":           payoutRef,
			"to_amount_actual":     trade.ToAmountExpected,
			"reason":               firstNonEmptyValue(resolutionNote, "Payout marked complete by admin"),
			"clear_dispute_reason": true,
		}, "admin"); err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, fmt.Errorf("unsupported_resolution_mode")
	}

	resolvedDispute, err := closeTradeDisputeTx(ctx, tx, dispute, resolutionMode, resolutionNote, resolver)
	if err != nil {
		return nil, nil, err
	}
	if err := s.enqueueTradeDisputeNotificationTx(ctx, tx, trade, resolvedDispute, "trade.dispute_resolved"); err != nil {
		return nil, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return resolvedDispute, trade, nil
}

func retryStatusForTrade(trade *domain.Trade) string {
	if trade == nil {
		return string(statemachine.TradeDepositConfirmed)
	}
	if trade.ExchangeOrderID != nil || trade.ToAmountActual != nil || trade.GraphPayoutID != nil || trade.GraphConversionID != nil {
		return string(statemachine.TradeConversionCompleted)
	}
	return string(statemachine.TradeDepositConfirmed)
}

func (s *ApplicationService) GetTradeStatusContext(ctx context.Context, userID string) (*domain.TradeStatusContext, error) {
	parsedUserID, err := uuid.Parse(strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}

	activeTrade, err := s.getFirstBlockingActiveTrade(ctx, parsedUserID)
	if err != nil {
		return nil, err
	}
	if activeTrade != nil {
		contextResult := &domain.TradeStatusContext{
			ContextType: "active",
			Trade:       activeTrade,
		}
		if strings.EqualFold(activeTrade.Status, string(statemachine.TradeDispute)) {
			contextResult.Dispute, err = s.getLatestTradeDisputeForTrade(ctx, activeTrade.ID)
			if err != nil {
				return nil, err
			}
		}
		return contextResult, nil
	}

	recentTrade, err := s.getMostRecentRelevantTrade(ctx, parsedUserID)
	if err != nil {
		return nil, err
	}
	if recentTrade == nil {
		return nil, nil
	}

	contextResult := &domain.TradeStatusContext{
		ContextType: "recent",
		Trade:       recentTrade,
	}
	if recentTrade.Status == string(statemachine.TradePayoutCompleted) {
		receipt, err := s.GetTradeReceipt(ctx, recentTrade.ID.String())
		if err != nil {
			return nil, err
		}
		contextResult.Receipt = receipt
	}
	if recentTrade.Status == string(statemachine.TradeDispute) || recentTrade.Status == string(statemachine.TradeDisputeClosed) {
		contextResult.Dispute, err = s.getLatestTradeDisputeForTrade(ctx, recentTrade.ID)
		if err != nil {
			return nil, err
		}
	}
	return contextResult, nil
}

func (s *ApplicationService) getMostRecentRelevantTrade(ctx context.Context, userID uuid.UUID) (*domain.Trade, error) {
	trade := &domain.Trade{}
	err := scanTrade(
		s.db.QueryRow(ctx, `
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
            WHERE user_id = $1::uuid
              AND status = ANY($2::trade_status[])
            ORDER BY updated_at DESC
            LIMIT 1
        `, userID, []string{
			string(statemachine.TradeDispute),
			string(statemachine.TradePayoutFailed),
			string(statemachine.TradeDisputeClosed),
			string(statemachine.TradePayoutCompleted),
			string(statemachine.TradeCancelled),
		}),
		trade,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return trade, nil
}

func stringValuePointer(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
