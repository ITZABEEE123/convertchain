package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/domain"
	graphclient "convert-chain/go-engine/internal/graph"
	"convert-chain/go-engine/internal/statemachine"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *ApplicationService) CreateOrGetUser(ctx context.Context, req dto.CreateUserRequest) (*domain.User, bool, error) {
	channelType, err := normalizeChannelType(req.ChannelType)
	if err != nil {
		return nil, false, err
	}

	user, err := s.getUserByChannel(ctx, channelType, req.ChannelUserID)
	if err == nil {
		if user.DeletionSubjectHash == nil || strings.TrimSpace(*user.DeletionSubjectHash) == "" {
			hash := deletionSubjectHash(channelType, req.ChannelUserID)
			if _, updateErr := s.db.Exec(ctx, `UPDATE users SET deletion_subject_hash = $2 WHERE id = $1::uuid`, user.ID, hash); updateErr == nil {
				user.DeletionSubjectHash = &hash
			}
		}
		return user, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	inserted := &domain.User{}
	err = scanUser(
		s.db.QueryRow(ctx, `
            INSERT INTO users (
                channel_type,
                channel_user_id,
                deletion_subject_hash,
                phone_number,
                first_name,
                status
            ) VALUES ($1::channel_type, $2, $3, NULLIF($4, ''), NULLIF($5, ''), 'UNREGISTERED')
            RETURNING
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
        `,
			channelType,
			req.ChannelUserID,
			deletionSubjectHash(channelType, req.ChannelUserID),
			req.PhoneNumber,
			req.Username,
		),
		inserted,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			user, getErr := s.getUserByChannel(ctx, channelType, req.ChannelUserID)
			if getErr == nil {
				return user, false, nil
			}
		}
		return nil, false, err
	}

	return inserted, true, nil
}

func (s *ApplicationService) RecordConsent(ctx context.Context, userID, version string, consentedAt time.Time) error {
	user, err := s.getUserByID(ctx, userID)
	if err != nil {
		return err
	}

	user.ConsentGivenAt = &consentedAt
	if user.Status == string(statemachine.StateUnregistered) {
		if err := s.userFSM.Transition(ctx, user, statemachine.EventConsentGiven); err != nil {
			return err
		}
	}

	_, err = s.db.Exec(ctx, `
        UPDATE users
        SET consent_given_at = $2,
            status = $3::user_status
        WHERE id = $1::uuid
    `, userID, consentedAt.UTC(), user.Status)
	return err
}

func (s *ApplicationService) SubmitKYC(ctx context.Context, req dto.KYCSubmitRequest) (*domain.KYCStatusSummary, error) {
	return s.submitKYCWorkflow(ctx, req)
}

func (s *ApplicationService) GetUserKYCStatus(ctx context.Context, userID string) (string, error) {
	user, err := s.getUserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	return userStatusToAPI(user.Status), nil
}

func (s *ApplicationService) ListBanks(ctx context.Context) ([]*domain.BankDirectoryEntry, error) {
	staticBanks := defaultBankDirectory()

	if s.graph == nil {
		return staticBanks, nil
	}

	banks, err := s.graph.ListBanks(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("failed to load provider bank directory; falling back to bundled directory", "error", err)
		}
		return staticBanks, nil
	}

	providerBanks := make([]*domain.BankDirectoryEntry, 0, len(banks))
	for _, bank := range banks {
		providerBanks = append(providerBanks, &domain.BankDirectoryEntry{
			BankID:   bank.ID,
			BankCode: strings.TrimSpace(bank.Code),
			BankName: strings.TrimSpace(bank.Name),
		})
	}

	merged := mergeBankDirectories(staticBanks, providerBanks)
	if s.graph.IsSandbox() {
		return withSandboxTestBank(merged), nil
	}
	return merged, nil
}

func (s *ApplicationService) ResolveBankAccount(ctx context.Context, bankCode, accountNumber string) (*domain.BankAccountResolution, error) {
	if s.graph == nil {
		return nil, errors.New("graph payout service is not configured")
	}
	if s.graph.IsSandbox() {
		return resolveSandboxBankAccount(bankCode, accountNumber)
	}

	resolved, err := s.graph.ResolveBankAccount(ctx, bankCode, accountNumber)
	if err != nil {
		return nil, err
	}

	bankName := strings.TrimSpace(resolved.BankName)
	if bankName == "" {
		bankName = bankNameFromCode(bankCode)
	}

	return &domain.BankAccountResolution{
		BankID:        strings.TrimSpace(resolved.BankID),
		BankCode:      strings.TrimSpace(resolved.BankCode),
		BankName:      bankName,
		AccountNumber: strings.TrimSpace(resolved.AccountNumber),
		AccountName:   strings.TrimSpace(resolved.AccountName),
	}, nil
}

func (s *ApplicationService) AddBankAccount(ctx context.Context, req dto.AddBankAccountRequest) (*domain.BankAccount, error) {
	if _, err := s.getUserByID(ctx, req.UserID); err != nil {
		return nil, err
	}
	if s.graph == nil {
		return nil, errors.New("graph payout service is not configured")
	}

	var resolved *domain.BankAccountResolution
	var payoutDestination *graphclient.PayoutDestination
	var graphDestID string

	if s.graph.IsSandbox() {
		s.logger.Info("sandbox mode: resolving bank account locally without provider verification",
			"user_id", req.UserID, "bank_code", req.BankCode, "account_number", req.AccountNumber)
		var err error
		resolved, err = resolveSandboxBankAccount(req.BankCode, req.AccountNumber)
		if err != nil {
			return nil, fmt.Errorf("resolve sandbox bank account: %w", err)
		}
		graphDestID = makeSandboxDestinationID(resolved.BankCode, resolved.AccountNumber)
	} else {
		var err error
		resolved, err = s.ResolveBankAccount(ctx, req.BankCode, req.AccountNumber)
		if err != nil {
			return nil, fmt.Errorf("resolve bank account: %w", err)
		}

		walletAccount, err := s.graph.GetWalletAccountByCurrency(ctx, "NGN")
		if err != nil {
			return nil, fmt.Errorf("load NGN wallet account: %w", err)
		}

		destinationLabel := fmt.Sprintf("convertchain-%s-%s", req.UserID[:8], resolved.AccountNumber)
		payoutDestination, err = s.graph.CreatePayoutDestination(ctx, graphclient.CreatePayoutDestinationRequest{
			AccountID:       walletAccount.ID,
			SourceType:      "wallet_account",
			Label:           destinationLabel,
			Type:            "nip",
			AccountType:     "personal",
			BankCode:        resolved.BankCode,
			BankID:          resolved.BankID,
			AccountNumber:   resolved.AccountNumber,
			BeneficiaryName: resolved.AccountName,
		})
		if err != nil {
			return nil, fmt.Errorf("create payout destination: %w", err)
		}
		graphDestID = strings.TrimSpace(payoutDestination.ID)
	}

	bankName := resolved.BankName
	if payoutDestination != nil && payoutDestination.BankName != "" {
		bankName = payoutDestination.BankName
	}
	if bankName == "" {
		bankName = bankNameFromCode(req.BankCode)
	}

	accountName := resolved.AccountName
	if payoutDestination != nil && payoutDestination.AccountName != "" {
		accountName = payoutDestination.AccountName
	}
	if accountName == "" {
		accountName = strings.TrimSpace(req.AccountName)
	}
	if accountName == "" {
		return nil, errors.New("verified account name is required")
	}

	isPrimary, err := s.shouldMakeBankAccountPrimary(ctx, req.UserID)
	if err != nil {
		return nil, err
	}

	account := &domain.BankAccount{}
	err = scanBankAccount(
		s.db.QueryRow(ctx, `
            INSERT INTO bank_accounts (
                user_id,
                bank_code,
                account_number,
                account_name,
                bank_name,
                graph_dest_id,
                is_primary,
                is_verified,
                is_active
            ) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, TRUE, TRUE)
            RETURNING
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
        `,
			req.UserID,
			resolved.BankCode,
			resolved.AccountNumber,
			accountName,
			bankName,
			graphDestID,
			isPrimary,
		),
		account,
	)
	if err != nil {
		return nil, err
	}

	return account, nil
}

func (s *ApplicationService) ListBankAccounts(ctx context.Context, userID string) ([]*domain.BankAccount, error) {
	rows, err := s.db.Query(ctx, `
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
        WHERE user_id = $1::uuid AND is_active = TRUE
        ORDER BY is_primary DESC, created_at ASC
    `, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	accounts := make([]*domain.BankAccount, 0)
	for rows.Next() {
		account := &domain.BankAccount{}
		if err := scanBankAccount(rows, account); err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func (s *ApplicationService) RaiseDispute(ctx context.Context, req dto.DisputeRequest) (*domain.DisputeRecord, error) {
	trade, err := s.getTradeByID(ctx, req.TradeID)
	if err != nil {
		return nil, err
	}
	if trade == nil {
		return nil, errors.New("trade_not_found")
	}

	note := req.Reason
	if req.Description != "" {
		note = fmt.Sprintf("%s: %s", req.Reason, req.Description)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if err := s.updateTradeStatusTx(ctx, tx, trade, string(statemachine.TradeDispute), map[string]interface{}{
		"reason":         note,
		"dispute_source": disputeSourceUser,
	}, "user"); err != nil {
		return nil, err
	}

	dispute, err := getOpenTradeDisputeTx(ctx, tx, trade.ID)
	if err != nil {
		return nil, err
	}
	if dispute == nil {
		return nil, fmt.Errorf("failed to create dispute record")
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &domain.DisputeRecord{
		ID:        dispute.ID.String(),
		TradeID:   req.TradeID,
		CreatedAt: dispute.CreatedAt,
		TicketRef: dispute.TicketRef,
	}, nil
}

func (s *ApplicationService) encryptPII(value string) (string, error) {
	if s.encryptor == nil {
		return value, nil
	}
	return s.encryptor.EncryptIfNotEmpty(value)
}
