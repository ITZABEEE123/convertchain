package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"convert-chain/go-engine/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type rowScanner interface {
	Scan(dest ...any) error
}

func (s *ApplicationService) getUserByChannel(ctx context.Context, channelType, channelUserID string) (*domain.User, error) {
	user := &domain.User{}
	err := scanUser(
		s.db.QueryRow(ctx, `
			SELECT
				id,
				channel_type::text,
				channel_user_id,
				COALESCE(phone_number, ''),
				COALESCE(email, ''),
				COALESCE(first_name, ''),
				COALESCE(last_name, ''),
				date_of_birth::text,
				status::text,
				kyc_tier::text,
				graph_person_id,
				txn_password_hash,
				txn_password_set_at,
				txn_password_failed_attempts,
				txn_password_locked_until,
				deleted_at,
				anonymized_at,
				deletion_subject_hash,
				consent_given_at,
				host(consent_ip)::text,
				is_active,
				created_at,
				updated_at
			FROM users
			WHERE channel_type = $1::channel_type
			  AND channel_user_id = $2
		`, channelType, channelUserID),
		user,
	)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *ApplicationService) getUserByID(ctx context.Context, userID string) (*domain.User, error) {
	user := &domain.User{}
	err := scanUser(
		s.db.QueryRow(ctx, `
			SELECT
				id,
				channel_type::text,
				channel_user_id,
				COALESCE(phone_number, ''),
				COALESCE(email, ''),
				COALESCE(first_name, ''),
				COALESCE(last_name, ''),
				date_of_birth::text,
				status::text,
				kyc_tier::text,
				graph_person_id,
				txn_password_hash,
				txn_password_set_at,
				txn_password_failed_attempts,
				txn_password_locked_until,
				deleted_at,
				anonymized_at,
				deletion_subject_hash,
				consent_given_at,
				host(consent_ip)::text,
				is_active,
				created_at,
				updated_at
			FROM users
			WHERE id = $1::uuid
		`, userID),
		user,
	)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *ApplicationService) getLatestKYCDocument(ctx context.Context, userID string) (*domain.KYCDocument, error) {
	doc := &domain.KYCDocument{}
	err := scanKYCDocument(
		s.db.QueryRow(ctx, `
			SELECT
				id,
				user_id,
				doc_type::text,
				document_number,
				file_url,
				provider,
				provider_ref,
				verified,
				verified_at,
				rejected_reason,
				expires_at::text,
				created_at
			FROM kyc_documents
			WHERE user_id = $1::uuid
			ORDER BY created_at DESC
			LIMIT 1
		`, userID),
		doc,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return doc, nil
}

func (s *ApplicationService) getQuoteByID(ctx context.Context, quoteID string) (*domain.Quote, error) {
	quote := &domain.Quote{}
	err := scanQuote(
		s.db.QueryRow(ctx, `
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
			WHERE id = $1::uuid
		`, quoteID),
		quote,
	)
	if err != nil {
		return nil, err
	}
	return quote, nil
}

func (s *ApplicationService) getTradeByID(ctx context.Context, tradeID string) (*domain.Trade, error) {
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
			WHERE id = $1::uuid
		`, tradeID),
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

func (s *ApplicationService) getTradeByGraphPayoutID(ctx context.Context, payoutID string) (*domain.Trade, error) {
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
			WHERE graph_payout_id = $1
			ORDER BY updated_at DESC
			LIMIT 1
		`, payoutID),
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
func (s *ApplicationService) getBankAccountByID(ctx context.Context, bankAccountID string) (*domain.BankAccount, error) {
	account := &domain.BankAccount{}
	err := scanBankAccount(
		s.db.QueryRow(ctx, `
			SELECT
				id,
				user_id,
				bank_code,
				account_number,
				account_name,
				bank_name,
				graph_dest_id,
				is_primary,
				is_verified,
				is_active,
				created_at
			FROM bank_accounts
			WHERE id = $1::uuid AND is_active = TRUE
		`, bankAccountID),
		account,
	)
	if err != nil {
		return nil, err
	}
	return account, nil
}

func (s *ApplicationService) getBankAccountByIDOrArchived(ctx context.Context, bankAccountID string) (*domain.BankAccount, error) {
	account := &domain.BankAccount{}
	err := scanBankAccount(
		s.db.QueryRow(ctx, `
			SELECT
				id,
				user_id,
				bank_code,
				account_number,
				account_name,
				bank_name,
				graph_dest_id,
				is_primary,
				is_verified,
				is_active,
				created_at
			FROM bank_accounts
			WHERE id = $1::uuid
		`, bankAccountID),
		account,
	)
	if err != nil {
		return nil, err
	}
	return account, nil
}

func (s *ApplicationService) shouldMakeBankAccountPrimary(ctx context.Context, userID string) (bool, error) {
	var count int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM bank_accounts WHERE user_id = $1::uuid AND is_active = TRUE`, userID).Scan(&count); err != nil {
		return false, err
	}
	return count == 0, nil
}

func scanUser(src rowScanner, user *domain.User) error {
	return src.Scan(
		&user.ID,
		&user.ChannelType,
		&user.ChannelUserID,
		&user.PhoneNumber,
		&user.Email,
		&user.FirstName,
		&user.LastName,
		&user.DateOfBirth,
		&user.Status,
		&user.KYCTier,
		&user.GraphPersonID,
		&user.TxnPasswordHash,
		&user.TxnPasswordSetAt,
		&user.TxnPasswordFailedAttempts,
		&user.TxnPasswordLockedUntil,
		&user.DeletedAt,
		&user.AnonymizedAt,
		&user.DeletionSubjectHash,
		&user.ConsentGivenAt,
		&user.ConsentIP,
		&user.IsActive,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
}

func scanKYCDocument(src rowScanner, doc *domain.KYCDocument) error {
	return src.Scan(
		&doc.ID,
		&doc.UserID,
		&doc.DocType,
		&doc.DocumentNumber,
		&doc.FileURL,
		&doc.Provider,
		&doc.ProviderRef,
		&doc.Verified,
		&doc.VerifiedAt,
		&doc.RejectedReason,
		&doc.ExpiresAt,
		&doc.CreatedAt,
	)
}

func scanQuote(src rowScanner, quote *domain.Quote) error {
	return src.Scan(
		&quote.ID,
		&quote.UserID,
		&quote.FromCurrency,
		&quote.ToCurrency,
		&quote.FromAmount,
		&quote.ToAmount,
		&quote.NetAmount,
		&quote.ExchangeRate,
		&quote.FiatRate,
		&quote.FeeBPS,
		&quote.FeeAmount,
		&quote.ValidUntil,
		&quote.AcceptedAt,
		&quote.ExpiredAt,
		&quote.CreatedAt,
	)
}

func scanTrade(src rowScanner, trade *domain.Trade) error {
	return src.Scan(
		&trade.ID,
		&trade.TradeRef,
		&trade.UserID,
		&trade.QuoteID,
		&trade.BankAccID,
		&trade.Status,
		&trade.FromCurrency,
		&trade.ToCurrency,
		&trade.FromAmount,
		&trade.ToAmountExpected,
		&trade.ToAmountActual,
		&trade.FeeAmount,
		&trade.DepositAddress,
		&trade.DepositTxHash,
		&trade.DepositConfirmedAt,
		&trade.ExchangeOrderID,
		&trade.GraphConversionID,
		&trade.GraphPayoutID,
		&trade.PayoutAuthorizedAt,
		&trade.PayoutAuthorizationMethod,
		&trade.DisputeReason,
		&trade.ExpiresAt,
		&trade.CompletedAt,
		&trade.CreatedAt,
		&trade.UpdatedAt,
	)
}

func scanBankAccount(src rowScanner, account *domain.BankAccount) error {
	return src.Scan(
		&account.ID,
		&account.UserID,
		&account.BankCode,
		&account.AccountNumber,
		&account.AccountName,
		&account.BankName,
		&account.GraphDestID,
		&account.IsPrimary,
		&account.IsVerified,
		&account.IsActive,
		&account.CreatedAt,
	)
}

func insertTradeHistory(ctx context.Context, tx pgx.Tx, tradeID uuid.UUID, fromStatus *string, toStatus string, actor string, note string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO trade_status_history (trade_id, from_status, to_status, actor, note)
		VALUES ($1::uuid, $2::trade_status, $3::trade_status, $4, NULLIF($5, ''))
	`, tradeID, fromStatus, toStatus, actor, strings.TrimSpace(note))
	return err
}

func parseOptionalTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copyValue := value.UTC()
	return &copyValue
}
