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
)

const (
	accountDeletionWeeklyLimit = 3
	accountDeletionWindowDays  = 7
)

var activeDeletionBlockingStatuses = []string{
	"PENDING_DEPOSIT",
	"DEPOSIT_RECEIVED",
	"DEPOSIT_CONFIRMED",
	"CONVERSION_IN_PROGRESS",
	"CONVERSION_COMPLETED",
	"PAYOUT_PENDING",
	"DISPUTE",
}

type ActiveTradeBlockError struct {
	TradeID  string
	TradeRef string
	Status   string
}

func (e *ActiveTradeBlockError) Error() string {
	return "active_trade_exists"
}

func (s *ApplicationService) GetDeletionQuota(ctx context.Context, userID string) (int, error) {
	user, err := s.getUserByID(ctx, userID)
	if err != nil {
		return 0, err
	}

	subjectHash := deletionHashForUser(user)
	used, err := s.countDeletionEvents(ctx, subjectHash, time.Now().UTC().AddDate(0, 0, -accountDeletionWindowDays))
	if err != nil {
		return 0, err
	}

	remaining := accountDeletionWeeklyLimit - used
	if remaining < 0 {
		remaining = 0
	}
	return remaining, nil
}

func (s *ApplicationService) DeleteAccount(ctx context.Context, userID, confirmationText, transactionPassword string) (time.Time, error) {
	if strings.ToUpper(strings.TrimSpace(confirmationText)) != "DELETE" {
		return time.Time{}, errors.New("delete_confirmation_required")
	}

	user, err := s.getUserByID(ctx, userID)
	if err != nil {
		return time.Time{}, err
	}
	if !user.IsActive {
		return time.Time{}, errors.New("account_inactive")
	}

	subjectHash := deletionHashForUser(user)
	used, err := s.countDeletionEvents(ctx, subjectHash, time.Now().UTC().AddDate(0, 0, -accountDeletionWindowDays))
	if err != nil {
		return time.Time{}, err
	}
	if used >= accountDeletionWeeklyLimit {
		return time.Time{}, errors.New("deletion_quota_exceeded")
	}

	if err := s.verifyTransactionPassword(ctx, buildPasswordState(user), transactionPassword); err != nil {
		return time.Time{}, err
	}

	blockingTrade, err := s.getFirstBlockingActiveTrade(ctx, user.ID)
	if err != nil {
		return time.Time{}, err
	}
	if blockingTrade != nil {
		return time.Time{}, &ActiveTradeBlockError{
			TradeID:  blockingTrade.ID.String(),
			TradeRef: blockingTrade.TradeRef,
			Status:   blockingTrade.Status,
		}
	}

	now := time.Now().UTC()
	tombstone := fmt.Sprintf("deleted:%s:%d", user.ID.String(), now.Unix())
	metadata, _ := json.Marshal(map[string]interface{}{
		"channel_type": user.ChannelType,
		"deleted_by":   "user",
	})

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO account_deletion_events (
			user_id,
			deletion_subject_hash,
			requested_at,
			completed_at,
			status,
			reason,
			metadata
		) VALUES ($1::uuid, $2, $3, $3, 'COMPLETED', 'user_requested', $4::jsonb)
	`, user.ID, subjectHash, now, string(metadata))
	if err != nil {
		return time.Time{}, err
	}

	_, err = tx.Exec(ctx, `
		UPDATE kyc_documents
		SET document_number = NULL,
			file_url = NULL,
			provider_ref = NULL
		WHERE user_id = $1::uuid
	`, user.ID)
	if err != nil {
		return time.Time{}, err
	}

	_, err = tx.Exec(ctx, `
		UPDATE bank_accounts
		SET account_number = '0000000000',
			account_name = 'Deleted Account',
			graph_dest_id = NULL,
			is_primary = FALSE,
			is_verified = FALSE,
			is_active = FALSE
		WHERE user_id = $1::uuid
	`, user.ID)
	if err != nil {
		return time.Time{}, err
	}

	_, err = tx.Exec(ctx, `
		UPDATE users
		SET phone_number = NULL,
			email = NULL,
			first_name = 'Deleted',
			last_name = 'User',
			date_of_birth = NULL,
			graph_person_id = NULL,
			channel_user_id = $2,
			txn_password_hash = NULL,
			txn_password_set_at = NULL,
			txn_password_failed_attempts = 0,
			txn_password_locked_until = NULL,
			is_active = FALSE,
			deleted_at = $3,
			anonymized_at = $3
		WHERE id = $1::uuid
	`, user.ID, tombstone, now)
	if err != nil {
		return time.Time{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return time.Time{}, err
	}

	return now, nil
}

func (s *ApplicationService) countDeletionEvents(ctx context.Context, subjectHash string, since time.Time) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM account_deletion_events
		WHERE deletion_subject_hash = $1
		  AND status = 'COMPLETED'
		  AND requested_at >= $2
	`, subjectHash, since).Scan(&count)
	return count, err
}

func (s *ApplicationService) getFirstBlockingActiveTrade(ctx context.Context, userID uuid.UUID) (*domain.Trade, error) {
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
		`, userID, activeDeletionBlockingStatuses),
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

func deletionHashForUser(user *domain.User) string {
	if user == nil {
		return ""
	}
	if user.DeletionSubjectHash != nil && strings.TrimSpace(*user.DeletionSubjectHash) != "" {
		return strings.TrimSpace(*user.DeletionSubjectHash)
	}
	return deletionSubjectHash(user.ChannelType, user.ChannelUserID)
}
