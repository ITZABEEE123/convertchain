package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"convert-chain/go-engine/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	notificationClaimTTL    = 2 * time.Minute
	notificationMaxAttempts = 8
)

func (s *ApplicationService) enqueueNotificationTx(
	ctx context.Context,
	tx pgx.Tx,
	userID uuid.UUID,
	tradeID *uuid.UUID,
	eventType string,
	payload map[string]interface{},
	dedupeKey string,
) error {
	if strings.TrimSpace(eventType) == "" || strings.TrimSpace(dedupeKey) == "" {
		return nil
	}

	var channelType string
	var recipientID string
	if err := tx.QueryRow(ctx, `
		SELECT channel_type::text, channel_user_id
		FROM users
		WHERE id = $1::uuid
	`, userID).Scan(&channelType, &recipientID); err != nil {
		return err
	}

	payloadCopy := map[string]interface{}{}
	for key, value := range payload {
		payloadCopy[key] = value
	}
	payloadCopy["recipient_id"] = recipientID

	encoded, err := json.Marshal(payloadCopy)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO notification_events (
			user_id,
			channel_type,
			trade_id,
			event_type,
			payload,
			dedupe_key
		) VALUES ($1::uuid, $2::channel_type, $3::uuid, $4, $5::jsonb, $6)
		ON CONFLICT (dedupe_key) DO NOTHING
	`, userID, channelType, tradeID, eventType, string(encoded), dedupeKey)
	return err
}

func (s *ApplicationService) enqueueTradeNotificationTx(
	ctx context.Context,
	tx pgx.Tx,
	trade *domain.Trade,
	newStatus string,
	metadata map[string]interface{},
) error {
	eventType, payload, dedupeKey, err := s.buildTradeNotificationTx(ctx, tx, trade, newStatus, metadata)
	if err != nil || eventType == "" {
		return err
	}
	return s.enqueueNotificationTx(ctx, tx, trade.UserID, &trade.ID, eventType, payload, dedupeKey)
}

func (s *ApplicationService) buildTradeNotificationTx(
	ctx context.Context,
	tx pgx.Tx,
	trade *domain.Trade,
	newStatus string,
	metadata map[string]interface{},
) (string, map[string]interface{}, string, error) {
	status := strings.ToUpper(strings.TrimSpace(newStatus))
	if trade == nil {
		return "", nil, "", nil
	}

	payload := map[string]interface{}{
		"trade_id":               trade.ID.String(),
		"trade_ref":              trade.TradeRef,
		"status":                 status,
		"asset":                  trade.FromCurrency,
		"from_amount":            trade.FromAmount,
		"net_amount_kobo":        trade.ToAmountExpected,
		"fee_amount_kobo":        trade.FeeAmount,
		"pricing_mode":           "sandbox_live_rates",
		"required_confirmations": 2,
	}

	if trade.GraphPayoutID != nil {
		payload["payout_ref"] = *trade.GraphPayoutID
	}
	if raw, ok := metadata["payout_ref"].(string); ok && strings.TrimSpace(raw) != "" {
		payload["payout_ref"] = strings.TrimSpace(raw)
	}
	if raw, ok := metadata["tx_hash"].(string); ok && strings.TrimSpace(raw) != "" {
		payload["tx_hash"] = strings.TrimSpace(raw)
	}
	if raw, ok := metadata["confirmations"].(int); ok {
		payload["confirmations"] = raw
	}
	if raw, ok := metadata["confirmations"].(int64); ok {
		payload["confirmations"] = int(raw)
	}
	if raw, ok := metadata["confirmations"].(float64); ok {
		payload["confirmations"] = int(raw)
	}
	if raw, ok := metadata["reason"].(string); ok && strings.TrimSpace(raw) != "" {
		payload["reason"] = strings.TrimSpace(raw)
	}

	if trade.BankAccID != nil {
		bankName, maskedNumber, err := loadTradeBankSummaryTx(ctx, tx, *trade.BankAccID)
		if err != nil {
			return "", nil, "", err
		}
		payload["bank_name"] = bankName
		payload["masked_account_number"] = maskedNumber
	}

	switch status {
	case "DEPOSIT_RECEIVED", "DEPOSIT_DETECTED":
		if _, ok := payload["confirmations"]; !ok {
			payload["confirmations"] = 1
		}
		return "trade.deposit_detected", payload, fmt.Sprintf("%s:%s:%s", trade.ID.String(), status, strings.TrimSpace(stringValueFromMap(payload, "tx_hash"))), nil
	case "DEPOSIT_CONFIRMED":
		if _, ok := payload["confirmations"]; !ok {
			payload["confirmations"] = 2
		}
		return "trade.deposit_confirmed", payload, fmt.Sprintf("%s:%s", trade.ID.String(), status), nil
	case "CONVERSION_IN_PROGRESS":
		return "trade.conversion_started", payload, fmt.Sprintf("%s:%s", trade.ID.String(), status), nil
	case "CONVERSION_COMPLETED":
		return "trade.conversion_completed", payload, fmt.Sprintf("%s:%s", trade.ID.String(), status), nil
	case "PAYOUT_PENDING":
		return "trade.payout_processing", payload, fmt.Sprintf("%s:%s", trade.ID.String(), status), nil
	case "PAYOUT_COMPLETED":
		return "trade.payout_completed", payload, fmt.Sprintf("%s:%s:%s", trade.ID.String(), status, strings.TrimSpace(stringValueFromMap(payload, "payout_ref"))), nil
	case "PAYOUT_FAILED":
		return "trade.payout_failed", payload, fmt.Sprintf("%s:%s", trade.ID.String(), status), nil
	default:
		return "", nil, "", nil
	}
}

func loadTradeBankSummaryTx(ctx context.Context, tx pgx.Tx, bankAccountID uuid.UUID) (string, string, error) {
	var bankName string
	var accountNumber string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(bank_name, ''), account_number
		FROM bank_accounts
		WHERE id = $1::uuid
	`, bankAccountID).Scan(&bankName, &accountNumber); err != nil {
		return "", "", err
	}
	return bankName, maskAccountNumber(accountNumber), nil
}

func (s *ApplicationService) GetPendingNotifications(ctx context.Context, channelType string, limit int) ([]domain.PendingNotification, error) {
	notifications, err := s.getPendingNotificationsWithDeliveryControls(ctx, channelType, limit)
	if err != nil {
		if isNotificationLegacySchemaError(err) {
			return s.getPendingNotificationsLegacy(ctx, channelType, limit)
		}
		return nil, err
	}
	return notifications, nil
}

func (s *ApplicationService) getPendingNotificationsWithDeliveryControls(ctx context.Context, channelType string, limit int) ([]domain.PendingNotification, error) {
	if limit <= 0 {
		limit = 50
	}

	claimToken := uuid.NewString()
	rows, err := s.db.Query(ctx, `
		WITH candidates AS (
			SELECT n.id
			FROM notification_events n
			WHERE n.delivered = FALSE
			  AND n.dead_lettered = FALSE
			  AND n.channel_type = $1::channel_type
			  AND n.next_attempt_at <= NOW()
			  AND (n.claimed_at IS NULL OR n.claimed_at <= NOW() - $3::interval)
			ORDER BY n.created_at ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		),
		claimed AS (
			UPDATE notification_events n
			SET claim_token = $4::uuid,
				claimed_at = NOW()
			FROM candidates c
			WHERE n.id = c.id
			RETURNING
				n.id,
				n.user_id,
				n.channel_type::text,
				n.trade_id,
				n.event_type,
				n.payload,
				n.created_at,
				n.delivery_attempts,
				n.claim_token::text
		)
		SELECT
			c.id,
			c.channel_type,
			COALESCE(c.payload->>'recipient_id', u.channel_user_id),
			COALESCE(c.trade_id::text, ''),
			c.event_type,
			c.payload,
			c.created_at,
			c.delivery_attempts,
			COALESCE(c.claim_token, '')
		FROM claimed c
		JOIN users u ON u.id = c.user_id
	`, strings.ToUpper(strings.TrimSpace(channelType)), limit, formatPGInterval(notificationClaimTTL), claimToken)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	notifications := make([]domain.PendingNotification, 0)
	for rows.Next() {
		var item domain.PendingNotification
		var payloadBytes []byte
		if err := rows.Scan(&item.ID, &item.ChannelType, &item.RecipientID, &item.TradeID, &item.EventType, &payloadBytes, &item.CreatedAt, &item.Attempts, &item.ClaimToken); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payloadBytes, &item.Payload); err != nil {
			return nil, err
		}
		notifications = append(notifications, item)
	}
	return notifications, rows.Err()
}

func (s *ApplicationService) AckNotification(ctx context.Context, notificationID string, delivered bool, deliveryError string, claimToken string) error {
	if delivered {
		if err := s.ackNotificationDelivered(ctx, notificationID, claimToken); err != nil {
			if isNotificationLegacySchemaError(err) {
				return s.ackNotificationDeliveredLegacy(ctx, notificationID)
			}
			return err
		}
		return nil
	}

	if err := s.ackNotificationFailed(ctx, notificationID, deliveryError, claimToken); err != nil {
		if isNotificationLegacySchemaError(err) {
			return s.ackNotificationFailedLegacy(ctx, notificationID, deliveryError)
		}
		return err
	}
	return nil
}

func (s *ApplicationService) ackNotificationDelivered(ctx context.Context, notificationID string, claimToken string) error {
	trimmedToken := strings.TrimSpace(claimToken)
	tag, err := s.db.Exec(ctx, `
		UPDATE notification_events
		SET delivered = TRUE,
			delivered_at = NOW(),
			delivery_error = NULL,
			claim_token = NULL,
			claimed_at = NULL
		WHERE id = $1::uuid
		  AND ($2 = '' OR claim_token::text = $2)
	`, notificationID, trimmedToken)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return &NotificationAckConflictError{
			NotificationID: notificationID,
			Message:        "notification acknowledgement rejected because the claim token is stale or does not match the current lease",
		}
	}
	return nil
}

func (s *ApplicationService) ackNotificationFailed(ctx context.Context, notificationID string, deliveryError string, claimToken string) error {
	trimmedToken := strings.TrimSpace(claimToken)
	tag, err := s.db.Exec(ctx, `
		UPDATE notification_events
		SET delivered = FALSE,
			delivered_at = NULL,
			delivery_error = NULLIF($3, ''),
			delivery_attempts = delivery_attempts + 1,
			next_attempt_at = CASE
				WHEN delivery_attempts + 1 >= $4 THEN NOW() + INTERVAL '24 hours'
				WHEN delivery_attempts + 1 >= 5 THEN NOW() + INTERVAL '5 minutes'
				WHEN delivery_attempts + 1 >= 3 THEN NOW() + INTERVAL '1 minute'
				ELSE NOW() + INTERVAL '15 seconds'
			END,
			dead_lettered = CASE WHEN delivery_attempts + 1 >= $4 THEN TRUE ELSE FALSE END,
			dead_lettered_at = CASE WHEN delivery_attempts + 1 >= $4 THEN NOW() ELSE dead_lettered_at END,
			claim_token = NULL,
			claimed_at = NULL
		WHERE id = $1::uuid
		  AND ($2 = '' OR claim_token::text = $2)
	`, notificationID, trimmedToken, strings.TrimSpace(deliveryError), notificationMaxAttempts)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return &NotificationAckConflictError{
			NotificationID: notificationID,
			Message:        "notification failure acknowledgement rejected because the claim token is stale or does not match the current lease",
		}
	}
	return nil
}

func (s *ApplicationService) GetNotificationMetrics(ctx context.Context, channelType string) (pending int, deadLetter int, err error) {
	pending, deadLetter, err = s.getNotificationMetricsWithDeliveryControls(ctx, channelType)
	if err != nil {
		if isNotificationLegacySchemaError(err) {
			return s.getNotificationMetricsLegacy(ctx, channelType)
		}
		return 0, 0, err
	}
	return pending, deadLetter, nil
}

func (s *ApplicationService) getNotificationMetricsWithDeliveryControls(ctx context.Context, channelType string) (pending int, deadLetter int, err error) {
	normalizedChannel := strings.ToUpper(strings.TrimSpace(channelType))
	if normalizedChannel == "" {
		normalizedChannel = "TELEGRAM"
	}

	err = s.db.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE delivered = FALSE AND dead_lettered = FALSE),
			COUNT(*) FILTER (WHERE dead_lettered = TRUE)
		FROM notification_events
		WHERE channel_type = $1::channel_type
	`, normalizedChannel).Scan(&pending, &deadLetter)
	if err != nil {
		return 0, 0, err
	}

	return pending, deadLetter, nil
}

func (s *ApplicationService) getPendingNotificationsLegacy(ctx context.Context, channelType string, limit int) ([]domain.PendingNotification, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.Query(ctx, `
		SELECT
			n.id,
			n.channel_type::text,
			COALESCE(n.payload->>'recipient_id', u.channel_user_id),
			COALESCE(n.trade_id::text, ''),
			n.event_type,
			n.payload,
			n.created_at
		FROM notification_events n
		JOIN users u ON u.id = n.user_id
		WHERE n.delivered = FALSE
		  AND n.channel_type = $1::channel_type
		ORDER BY n.created_at ASC
		LIMIT $2
	`, strings.ToUpper(strings.TrimSpace(channelType)), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	notifications := make([]domain.PendingNotification, 0)
	for rows.Next() {
		var item domain.PendingNotification
		var payloadBytes []byte
		if err := rows.Scan(&item.ID, &item.ChannelType, &item.RecipientID, &item.TradeID, &item.EventType, &payloadBytes, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payloadBytes, &item.Payload); err != nil {
			return nil, err
		}
		item.Attempts = 0
		item.ClaimToken = ""
		notifications = append(notifications, item)
	}

	return notifications, rows.Err()
}

func (s *ApplicationService) ackNotificationDeliveredLegacy(ctx context.Context, notificationID string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE notification_events
		SET delivered = TRUE,
			delivered_at = NOW(),
			delivery_error = NULL
		WHERE id = $1::uuid
	`, notificationID)
	return err
}

func (s *ApplicationService) ackNotificationFailedLegacy(ctx context.Context, notificationID string, deliveryError string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE notification_events
		SET delivered = FALSE,
			delivered_at = NULL,
			delivery_error = NULLIF($2, '')
		WHERE id = $1::uuid
	`, notificationID, strings.TrimSpace(deliveryError))
	return err
}

func (s *ApplicationService) getNotificationMetricsLegacy(ctx context.Context, channelType string) (pending int, deadLetter int, err error) {
	normalizedChannel := strings.ToUpper(strings.TrimSpace(channelType))
	if normalizedChannel == "" {
		normalizedChannel = "TELEGRAM"
	}

	err = s.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM notification_events
		WHERE channel_type = $1::channel_type
		  AND delivered = FALSE
	`, normalizedChannel).Scan(&pending)
	if err != nil {
		return 0, 0, err
	}

	return pending, 0, nil
}

func isNotificationLegacySchemaError(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42703" {
		return true
	}

	lowered := strings.ToLower(err.Error())
	return strings.Contains(lowered, "delivery_attempts") ||
		strings.Contains(lowered, "next_attempt_at") ||
		strings.Contains(lowered, "claim_token") ||
		strings.Contains(lowered, "claimed_at") ||
		strings.Contains(lowered, "dead_lettered")
}

func formatPGInterval(value time.Duration) string {
	if value <= 0 {
		return "0 seconds"
	}
	seconds := int(value.Seconds())
	return fmt.Sprintf("%d seconds", seconds)
}

func maskAccountNumber(value string) string {
	if len(value) <= 4 {
		return value
	}
	return "******" + value[len(value)-4:]
}

func stringValueFromMap(payload map[string]interface{}, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	return fmt.Sprintf("%v", value)
}
