package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

	if cached, ok := s.cachedBankDirectory(); ok {
		return cached, nil
	}

	if s.logger != nil {
		s.logger.Info("bank_list_fetch_started", "provider", "graph")
	}

	banks, err := s.graph.ListBanks(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("failed to load provider bank directory", "error", err)
		}
		if cached, ok := s.cachedBankDirectoryStale(); ok {
			if s.logger != nil {
				s.logger.Warn("using stale provider bank directory", "bank_count", len(cached))
			}
			return cached, nil
		}
		if strings.EqualFold(s.options.Environment, "production") {
			return nil, newBankResolveError(
				bankErrProviderUnavailable,
				"Bank directory is temporarily unavailable. Please try again shortly.",
				http.StatusBadGateway,
				nil,
				err,
			)
		}
		return staticBanks, nil
	}

	providerBanks := make([]*domain.BankDirectoryEntry, 0, len(banks))
	for _, bank := range banks {
		resolveCode := strings.TrimSpace(firstNonEmptyLocal(bank.ResolveBankCode, bank.NIPCode, bank.Code))
		providerBanks = append(providerBanks, &domain.BankDirectoryEntry{
			BankID:          strings.TrimSpace(bank.ID),
			ProviderBankID:  strings.TrimSpace(bank.ID),
			BankCode:        resolveCode,
			BankName:        strings.TrimSpace(bank.Name),
			Slug:            strings.TrimSpace(bank.Slug),
			NIPCode:         strings.TrimSpace(bank.NIPCode),
			ShortCode:       strings.TrimSpace(bank.ShortCode),
			Country:         strings.TrimSpace(bank.Country),
			Currency:        strings.ToUpper(strings.TrimSpace(bank.Currency)),
			ResolveBankCode: resolveCode,
		})
	}

	merged := providerBanks
	if len(merged) == 0 || s.graph.IsSandbox() {
		merged = mergeBankDirectories(staticBanks, providerBanks)
	}
	if s.graph.IsSandbox() {
		merged = withSandboxTestBank(merged)
	}
	s.setBankDirectoryCache(merged)
	if s.logger != nil {
		s.logger.Info("bank_list_fetch_succeeded", "provider", "graph", "bank_count", len(merged))
	}
	return merged, nil
}

func (s *ApplicationService) ResolveBankAccount(ctx context.Context, req dto.ResolveBankAccountRequest) (*domain.BankAccountResolution, error) {
	bankCode := strings.TrimSpace(req.BankCode)
	bankName := strings.TrimSpace(req.BankName)
	accountNumber := strings.TrimSpace(req.AccountNumber)
	currency := strings.ToUpper(strings.TrimSpace(req.Currency))
	if currency == "" {
		currency = "NGN"
	}

	if !isDigits(bankCode) || len(bankCode) < 3 || len(bankCode) > 6 {
		return nil, newBankResolveError(
			bankErrInvalidBankCode,
			"Please choose a valid bank before entering your account number.",
			http.StatusBadRequest,
			map[string]any{"bank_code": bankCode, "bank_name": bankName, "account_last4": accountLast4(accountNumber)},
			nil,
		)
	}
	if !isDigits(accountNumber) || len(accountNumber) != 10 {
		return nil, newBankResolveError(
			bankErrInvalidAccountNumber,
			"Please enter a valid 10-digit account number.",
			http.StatusBadRequest,
			map[string]any{"bank_code": bankCode, "bank_name": bankName, "account_last4": accountLast4(accountNumber)},
			nil,
		)
	}
	if s.graph == nil {
		return nil, newBankResolveError(
			bankErrProviderUnavailable,
			"Bank verification is temporarily unavailable. Please try again shortly.",
			http.StatusBadGateway,
			map[string]any{"bank_code": bankCode, "bank_name": bankName, "account_last4": accountLast4(accountNumber)},
			errors.New("graph payout service is not configured"),
		)
	}
	if s.graph.IsSandbox() {
		return resolveSandboxBankAccount(bankCode, accountNumber)
	}

	selectedBank := s.lookupProviderBankForResolve(ctx, req)
	if selectedBank != nil {
		bankName = firstNonEmptyLocal(bankName, selectedBank.BankName)
		resolveCode := strings.TrimSpace(firstNonEmptyLocal(selectedBank.ResolveBankCode, selectedBank.NIPCode, selectedBank.BankCode))
		if resolveCode != "" && resolveCode != bankCode {
			if s.logger != nil {
				s.logger.Info("bank_code_mapped_to_nip_code", "from_code", bankCode, "to_code", resolveCode, "bank_name", bankName)
			}
			bankCode = resolveCode
		}
	}

	if s.logger != nil {
		s.logger.Info("bank_account_resolve_started", "provider", "graph", "bank_code", bankCode, "bank_name", bankName, "account_last4", accountLast4(accountNumber), "currency", currency)
		s.logger.Info("graph_bank_resolve_request_started", "bank_code", bankCode, "bank_name", bankName, "account_last4", accountLast4(accountNumber), "currency", currency)
	}

	resolved, err := s.graph.ResolveBankAccount(ctx, bankCode, accountNumber, currency)
	if err != nil {
		safeErr := classifyBankResolveError(err, bankCode, bankName, accountNumber)
		if s.logger != nil {
			var details map[string]any
			if bankErr, ok := safeErr.(interface{ SafeDetails() map[string]any }); ok {
				details = bankErr.SafeDetails()
			}
			args := []any{"bank_code", bankCode, "bank_name", bankName, "account_last4", accountLast4(accountNumber), "bank_error_code", bankErrProviderError}
			if bankErr, ok := safeErr.(interface{ BankErrorCode() string }); ok {
				args = []any{"bank_code", bankCode, "bank_name", bankName, "account_last4", accountLast4(accountNumber), "bank_error_code", bankErr.BankErrorCode()}
			}
			if details != nil {
				for key, value := range details {
					if key == "bank_code" || key == "bank_name" || key == "account_last4" {
						continue
					}
					args = append(args, key, value)
				}
			}
			s.logger.Warn("graph_bank_resolve_failed", args...)
		}
		return nil, safeErr
	}

	if s.logger != nil {
		s.logger.Info("graph_bank_resolve_succeeded", "bank_code", bankCode, "bank_name", bankName, "account_last4", accountLast4(accountNumber))
	}

	resolvedBankName := strings.TrimSpace(resolved.BankName)
	if resolvedBankName == "" {
		resolvedBankName = bankName
	}
	if resolvedBankName == "" {
		bankName = bankNameFromCode(bankCode)
	}

	return &domain.BankAccountResolution{
		BankID:        strings.TrimSpace(resolved.BankID),
		BankCode:      firstNonEmptyLocal(strings.TrimSpace(resolved.BankCode), bankCode),
		BankName:      firstNonEmptyLocal(resolvedBankName, bankName),
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
			"user_id", req.UserID, "bank_code", req.BankCode, "account_last4", accountLast4(req.AccountNumber))
		var err error
		resolved, err = resolveSandboxBankAccount(req.BankCode, req.AccountNumber)
		if err != nil {
			return nil, fmt.Errorf("resolve sandbox bank account: %w", err)
		}
		graphDestID = makeSandboxDestinationID(resolved.BankCode, resolved.AccountNumber)
	} else {
		var err error
		resolved, err = s.ResolveBankAccount(ctx, dto.ResolveBankAccountRequest{
			UserID:         req.UserID,
			ProviderBankID: req.ProviderBankID,
			BankCode:       req.BankCode,
			BankName:       req.BankName,
			AccountNumber:  req.AccountNumber,
			Currency:       req.Currency,
		})
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
		bankName = strings.TrimSpace(req.BankName)
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

func (s *ApplicationService) cachedBankDirectory() ([]*domain.BankDirectoryEntry, bool) {
	s.bankDirectoryCacheMu.RLock()
	defer s.bankDirectoryCacheMu.RUnlock()

	if len(s.bankDirectoryCache) == 0 || time.Now().After(s.bankDirectoryCacheExpiresAt) {
		return nil, false
	}
	return cloneBankDirectory(s.bankDirectoryCache), true
}

func (s *ApplicationService) cachedBankDirectoryStale() ([]*domain.BankDirectoryEntry, bool) {
	s.bankDirectoryCacheMu.RLock()
	defer s.bankDirectoryCacheMu.RUnlock()

	if len(s.bankDirectoryCache) == 0 {
		return nil, false
	}
	return cloneBankDirectory(s.bankDirectoryCache), true
}

func (s *ApplicationService) setBankDirectoryCache(entries []*domain.BankDirectoryEntry) {
	s.bankDirectoryCacheMu.Lock()
	defer s.bankDirectoryCacheMu.Unlock()

	s.bankDirectoryCache = cloneBankDirectory(entries)
	s.bankDirectoryCacheExpiresAt = time.Now().Add(30 * time.Minute)
}

func cloneBankDirectory(entries []*domain.BankDirectoryEntry) []*domain.BankDirectoryEntry {
	copied := make([]*domain.BankDirectoryEntry, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		value := *entry
		copied = append(copied, &value)
	}
	return copied
}

func (s *ApplicationService) lookupProviderBankForResolve(ctx context.Context, req dto.ResolveBankAccountRequest) *domain.BankDirectoryEntry {
	banks, err := s.ListBanks(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("bank directory lookup failed during resolve", "error", err)
		}
		return nil
	}

	providerBankID := strings.TrimSpace(req.ProviderBankID)
	bankCode := strings.TrimSpace(req.BankCode)
	bankName := normalizeBankLookupValue(req.BankName)

	for _, bank := range banks {
		if bank == nil {
			continue
		}
		if providerBankID != "" && (strings.EqualFold(bank.ProviderBankID, providerBankID) || strings.EqualFold(bank.BankID, providerBankID)) {
			copyBank := *bank
			return &copyBank
		}
	}

	for _, bank := range banks {
		if bank == nil {
			continue
		}
		if bankName != "" && (normalizeBankLookupValue(bank.BankName) == bankName || normalizeBankLookupValue(bank.Slug) == bankName) {
			copyBank := *bank
			return &copyBank
		}
	}

	for _, bank := range banks {
		if bank == nil {
			continue
		}
		if bankCode != "" && (bankCode == strings.TrimSpace(bank.ResolveBankCode) || bankCode == strings.TrimSpace(bank.NIPCode) || bankCode == strings.TrimSpace(bank.BankCode) || bankCode == strings.TrimSpace(bank.ShortCode)) {
			copyBank := *bank
			return &copyBank
		}
	}

	return nil
}

func normalizeBankLookupValue(value string) string {
	parts := strings.Fields(strings.ToLower(strings.TrimSpace(value)))
	return strings.Join(parts, " ")
}
