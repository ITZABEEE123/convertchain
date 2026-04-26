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
	"convert-chain/go-engine/internal/kyc"
	"convert-chain/go-engine/internal/statemachine"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *ApplicationService) submitKYCWorkflow(ctx context.Context, req dto.KYCSubmitRequest) (*domain.KYCStatusSummary, error) {
	s.logKYCSubmitStage("kyc_submit_received", req.UserID)
	s.logKYCSubmitStage("kyc_submit_validation_passed", req.UserID)
	s.logKYCSubmitStage("kyc_user_lookup_started", req.UserID)

	user, err := s.getUserByID(ctx, req.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.logKYCSubmitStage("kyc_user_lookup_failed", req.UserID, "reason", "not_found")
			return nil, userNotFoundKYCError(err)
		}
		s.logKYCSubmitStage("kyc_user_lookup_failed", req.UserID, "reason", "db_error")
		return nil, err
	}
	s.logKYCSubmitStage("kyc_user_lookup_succeeded", req.UserID)

	tier := normalizeKYCTier(req.Tier)
	var summary *domain.KYCStatusSummary
	if tier == "TIER_1" {
		summary, err = s.submitTier1KYC(ctx, user, req)
	} else {
		summary, err = s.submitTier2PlusKYC(ctx, user, req, tier)
	}
	if err != nil {
		return nil, err
	}
	s.logKYCSubmitStage("kyc_submit_completed", req.UserID, "tier", tier)
	return summary, nil
}

func (s *ApplicationService) submitTier1KYC(ctx context.Context, user *domain.User, req dto.KYCSubmitRequest) (*domain.KYCStatusSummary, error) {
	if user.Status == string(statemachine.StateKYCApproved) {
		return nil, errors.New("kyc_already_approved")
	}
	if user.Status == string(statemachine.StateKYCPending) {
		return s.buildKYCStatusSummary(ctx, req.UserID)
	}

	phone := normalizePhoneNumber(req.PhoneNumber)
	workingCopy := *user
	workingCopy.PhoneNumber = phone
	if err := transitionUserToTier1Pending(ctx, s.userFSM, &workingCopy); err != nil {
		return nil, err
	}

	result, err := s.runTier1Provider(ctx, req, phone)
	if err != nil {
		return nil, err
	}

	switch strings.ToUpper(strings.TrimSpace(result.Status)) {
	case "APPROVED":
		if err := s.userFSM.Transition(ctx, &workingCopy, statemachine.EventKYCApproved); err != nil {
			return nil, err
		}
		workingCopy.KYCTier = "TIER_1"
	case "REJECTED":
		if err := s.userFSM.Transition(ctx, &workingCopy, statemachine.EventKYCRejected); err != nil {
			return nil, err
		}
		workingCopy.KYCTier = "TIER_0"
	default:
		workingCopy.KYCTier = "TIER_0"
		result.Status = "PENDING"
	}

	s.logKYCSubmitStage("kyc_record_upsert_started", req.UserID)
	if err := s.persistTier1KYCOutcome(ctx, &workingCopy, req, phone, result); err != nil {
		s.logKYCSubmitStage("kyc_record_upsert_failed", req.UserID, "reason", "db_error")
		return nil, err
	}
	s.logKYCSubmitStage("kyc_record_upsert_succeeded", req.UserID)

	summary, err := s.buildKYCStatusSummary(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	mergeKYCResultMetadata(summary, result)
	return summary, nil
}

func (s *ApplicationService) submitTier2PlusKYC(ctx context.Context, user *domain.User, req dto.KYCSubmitRequest, tier string) (*domain.KYCStatusSummary, error) {
	if user.Status != string(statemachine.StateKYCApproved) {
		return nil, errors.New("tier_upgrade_requires_approved_kyc")
	}

	if s.kycOrchestrator == nil || !s.kycOrchestrator.SupportsTier2() {
		return nil, providerConfigKYCError(
			"KYC provider is not configured",
			map[string]interface{}{"provider": "sumsub", "tier": tier},
			errors.New("kyc_provider_not_configured"),
		)
	}

	result, err := s.kycOrchestrator.SubmitTier2KYC(ctx, kyc.Tier2KYCRequest{
		UserID:               user.ID,
		TargetTier:           tier,
		LevelName:            s.sumsubLevelNameForTier(tier),
		FirstName:            req.FirstName,
		LastName:             req.LastName,
		DateOfBirth:          req.DateOfBirth,
		PhoneNumber:          normalizePhoneNumber(req.PhoneNumber),
		SelfieBase64:         req.SelfieBase64,
		ProofOfAddressBase64: req.ProofOfAddressBase64,
	})
	if err != nil {
		return nil, classifyKYCProviderError(err)
	}

	if err := s.persistAdvancedKYCSubmission(ctx, user.ID, req, result); err != nil {
		return nil, err
	}

	return &domain.KYCStatusSummary{
		UserID:          user.ID,
		Status:          strings.ToUpper(strings.TrimSpace(result.Status)),
		Tier:            tier,
		Provider:        result.Provider,
		ProviderRef:     result.ProviderRef,
		ProviderStatus:  result.ProviderStatus,
		LevelName:       result.LevelName,
		VerificationURL: result.VerificationURL,
		SubmittedAt:     timePointer(time.Now().UTC()),
		CompletedAt:     nil,
	}, nil
}

func (s *ApplicationService) runTier1Provider(ctx context.Context, req dto.KYCSubmitRequest, phone string) (*kyc.KYCResult, error) {
	switch normalizeKYCProvider(s.options.KYCPrimaryProvider) {
	case "sumsub":
		if s.kycOrchestrator != nil && s.kycOrchestrator.SupportsSumsub() {
			return s.submitSumsubTierKYC(ctx, req, phone, "TIER_1")
		}
		if s.options.AutoApproveKYC {
			return &kyc.KYCResult{Status: "APPROVED", Tier: "TIER_1", Provider: "mock-local"}, nil
		}
		return nil, providerConfigKYCError(
			"KYC provider is not configured",
			map[string]interface{}{"provider": "sumsub"},
			errors.New("kyc_provider_not_configured"),
		)
	default:
		return s.runSmileIDTier1Provider(ctx, req, phone)
	}
}

func (s *ApplicationService) runSmileIDTier1Provider(ctx context.Context, req dto.KYCSubmitRequest, phone string) (*kyc.KYCResult, error) {
	if s.kycOrchestrator != nil && s.kycOrchestrator.SupportsTier1() {
		result, err := s.kycOrchestrator.SubmitTier1KYC(ctx, kyc.Tier1KYCRequest{
			UserID:      uuid.MustParse(req.UserID),
			BVN:         req.BVN,
			NIN:         req.NIN,
			FirstName:   req.FirstName,
			LastName:    req.LastName,
			DateOfBirth: req.DateOfBirth,
			PhoneNumber: phone,
		})
		if err != nil {
			if s.options.AutoApproveKYC {
				if s.logger != nil {
					s.logger.Warn(
						"tier1 KYC provider failed in auto-approve mode; using mock-local approval",
						"user_id", req.UserID,
						"error", err,
					)
				}
				return &kyc.KYCResult{Status: "APPROVED", Tier: "TIER_1", Provider: "mock-local"}, nil
			}
			return nil, err
		}
		if s.options.AutoApproveKYC && result != nil && strings.EqualFold(result.Status, "REJECTED") {
			if s.logger != nil {
				s.logger.Warn(
					"tier1 KYC provider rejected in auto-approve mode; using mock-local approval",
					"user_id", req.UserID,
					"reason", result.Reason,
				)
			}
			return &kyc.KYCResult{Status: "APPROVED", Tier: "TIER_1", Provider: "mock-local"}, nil
		}
		return result, nil
	}

	if s.options.AutoApproveKYC {
		return &kyc.KYCResult{Status: "APPROVED", Tier: "TIER_1", Provider: "mock-local"}, nil
	}

	return nil, errors.New("kyc_provider_not_configured")
}

func (s *ApplicationService) submitSumsubTierKYC(ctx context.Context, req dto.KYCSubmitRequest, phone, tier string) (*kyc.KYCResult, error) {
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		return nil, newKYCSubmitError(dto.ErrCodeValidation, http.StatusBadRequest, "Invalid user_id", nil, err)
	}
	levelName := strings.TrimSpace(s.sumsubLevelNameForTier(tier))
	if levelName == "" {
		return nil, providerConfigKYCError(
			"Sumsub level name is not configured",
			map[string]interface{}{"provider": "sumsub", "tier": tier, "missing_env": sumsubLevelEnvName(tier)},
			errors.New("sumsub_level_name_missing"),
		)
	}
	if s.options.SumsubWebSDKLinkTTLSeconds <= 0 {
		return nil, providerConfigKYCError(
			"Sumsub WebSDK link TTL is invalid",
			map[string]interface{}{"provider": "sumsub", "tier": tier, "missing_env": "SUMSUB_WEBSDK_LINK_TTL_SECONDS"},
			errors.New("sumsub_websdk_ttl_invalid"),
		)
	}

	result, err := s.kycOrchestrator.SubmitSumsubKYC(ctx, kyc.SumsubKYCRequest{
		UserID:      userID,
		TargetTier:  tier,
		LevelName:   levelName,
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		DateOfBirth: req.DateOfBirth,
		PhoneNumber: phone,
		TTLInSecs:   s.options.SumsubWebSDKLinkTTLSeconds,
	})
	if err != nil {
		if s.options.AutoApproveKYC {
			if s.logger != nil {
				s.logger.Warn(
					"sumsub tier KYC failed in auto-approve mode; using mock-local approval",
					"user_id", req.UserID,
					"tier", tier,
					"error", err,
				)
			}
			return &kyc.KYCResult{Status: "APPROVED", Tier: tier, Provider: "mock-local"}, nil
		}
		return nil, classifyKYCProviderError(err)
	}
	return result, nil
}

func transitionUserToTier1Pending(ctx context.Context, fsm *statemachine.UserFSM, user *domain.User) error {
	switch user.Status {
	case string(statemachine.StateUnregistered):
		if user.ConsentGivenAt == nil {
			return errors.New("consent_required")
		}
		if err := fsm.Transition(ctx, user, statemachine.EventConsentGiven); err != nil {
			return err
		}
		fallthrough
	case string(statemachine.StateKYCInProgress):
		return fsm.Transition(ctx, user, statemachine.EventKYCSubmitted)
	case string(statemachine.StateKYCRejected):
		user.LastRejectedAt = nil
		if err := fsm.Transition(ctx, user, statemachine.EventKYCRetry); err != nil {
			return err
		}
		return fsm.Transition(ctx, user, statemachine.EventKYCSubmitted)
	case string(statemachine.StateKYCPending):
		return nil
	default:
		return fmt.Errorf("invalid user state %s for tier 1 submission", user.Status)
	}
}

func (s *ApplicationService) persistTier1KYCOutcome(ctx context.Context, user *domain.User, req dto.KYCSubmitRequest, phone string, result *kyc.KYCResult) error {
	encryptedNIN, err := s.encryptPII(req.NIN)
	if err != nil {
		return err
	}
	encryptedBVN, err := s.encryptPII(req.BVN)
	if err != nil {
		return err
	}

	provider := strings.TrimSpace(result.Provider)
	if provider == "" {
		provider = "smile_id"
	}
	providerRef := strings.TrimSpace(result.ProviderRef)
	verified, verifiedAt, rejectionReason := kycOutcomeFields(result)

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		UPDATE users
		SET first_name = $2,
			last_name = $3,
			phone_number = $4,
			date_of_birth = $5::date,
			status = $6::user_status,
			kyc_tier = $7::kyc_tier
		WHERE id = $1::uuid
	`, user.ID, req.FirstName, req.LastName, phone, req.DateOfBirth, user.Status, user.KYCTier)
	if err != nil {
		return err
	}

	for _, doc := range []struct {
		docType string
		value   *string
	}{
		{docType: "NIN", value: &encryptedNIN},
		{docType: "BVN", value: &encryptedBVN},
	} {
		_, err = tx.Exec(ctx, `
			INSERT INTO kyc_documents (
				user_id,
				doc_type,
				document_number,
				provider,
				provider_ref,
				verified,
				verified_at,
				rejected_reason
			) VALUES ($1::uuid, $2::kyc_doc_type, $3, $4, NULLIF($5, ''), $6, $7, NULLIF($8, ''))
		`, user.ID, doc.docType, doc.value, provider, providerRef, verified, verifiedAt, rejectionReason)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (s *ApplicationService) persistAdvancedKYCSubmission(ctx context.Context, userID uuid.UUID, req dto.KYCSubmitRequest, result *kyc.KYCResult) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	phone := normalizePhoneNumber(req.PhoneNumber)
	_, err = tx.Exec(ctx, `
		UPDATE users
		SET first_name = COALESCE(NULLIF($2, ''), first_name),
			last_name = COALESCE(NULLIF($3, ''), last_name),
			phone_number = COALESCE(NULLIF($4, ''), phone_number),
			date_of_birth = COALESCE(NULLIF($5, '')::date, date_of_birth)
		WHERE id = $1::uuid
	`, userID, req.FirstName, req.LastName, phone, req.DateOfBirth)
	if err != nil {
		return err
	}

	docTypes := []string{"SELFIE", "PROOF_OF_ADDRESS"}
	for _, docType := range docTypes {
		_, err = tx.Exec(ctx, `
			INSERT INTO kyc_documents (
				user_id,
				doc_type,
				provider,
				provider_ref,
				verified,
				verified_at,
				rejected_reason
			) VALUES ($1::uuid, $2::kyc_doc_type, $3, NULLIF($4, ''), NULL, NULL, NULL)
		`, userID, docType, result.Provider, result.ProviderRef)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (s *ApplicationService) GetKYCStatus(ctx context.Context, userID string) (*domain.KYCStatusSummary, error) {
	return s.buildKYCStatusSummary(ctx, userID)
}

func (s *ApplicationService) buildKYCStatusSummary(ctx context.Context, userID string) (*domain.KYCStatusSummary, error) {
	user, err := s.getUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	record, err := s.getLatestKYCDocument(ctx, userID)
	if err != nil {
		return nil, err
	}

	summary := &domain.KYCStatusSummary{
		UserID:                 user.ID,
		Status:                 userStatusToAPI(user.Status),
		Tier:                   user.KYCTier,
		TransactionPasswordSet: transactionPasswordSet(user),
	}
	if record == nil {
		return summary, nil
	}
	if record.Provider != nil {
		summary.Provider = *record.Provider
	}
	if record.ProviderRef != nil {
		summary.ProviderRef = *record.ProviderRef
	}
	summary.SubmittedAt = timePointer(record.CreatedAt.UTC())
	if record.VerifiedAt != nil {
		summary.CompletedAt = record.VerifiedAt
	}
	if record.RejectedReason != nil {
		summary.RejectionReason = *record.RejectedReason
	}
	if summary.Provider == "sumsub" && strings.EqualFold(summary.Status, "PENDING") {
		summary.LevelName = s.sumsubLevelNameForTier(summary.Tier)
		if s.kycOrchestrator != nil && s.kycOrchestrator.SupportsSumsub() {
			if link, err := s.kycOrchestrator.CreateSumsubVerificationLink(
				ctx,
				user.ID.String(),
				summary.LevelName,
				user.Email,
				user.PhoneNumber,
				s.options.SumsubWebSDKLinkTTLSeconds,
			); err == nil {
				summary.VerificationURL = link
			} else if s.logger != nil {
				s.logger.Warn("failed to regenerate sumsub verification link", "user_id", user.ID.String(), "error", err)
			}
		}
	}

	return summary, nil
}

func (s *ApplicationService) SaveKYCResult(ctx context.Context, userID uuid.UUID, tier string, status string) error {
	user, err := s.getUserByID(ctx, userID.String())
	if err != nil {
		return err
	}

	currentTier := normalizePersistedKYCTier(user.KYCTier)
	normalizedStatus := strings.ToUpper(strings.TrimSpace(status))
	normalizedTier := normalizeKYCTier(tier)
	isTierUpgrade := kycTierRank(normalizedTier) > kycTierRank(currentTier)

	switch normalizedStatus {
	case "APPROVED":
		if normalizedTier == "TIER_1" {
			if user.Status == string(statemachine.StateKYCPending) {
				if err := s.userFSM.Transition(ctx, user, statemachine.EventKYCApproved); err != nil {
					return err
				}
			} else {
				user.Status = string(statemachine.StateKYCApproved)
			}
			user.KYCTier = normalizedTier
		} else {
			user.Status = string(statemachine.StateKYCApproved)
			if isTierUpgrade {
				user.KYCTier = normalizedTier
			} else {
				user.KYCTier = currentTier
			}
		}
	case "REJECTED":
		if isTierUpgrade && currentTier != "TIER_0" {
			user.Status = string(statemachine.StateKYCApproved)
			user.KYCTier = currentTier
		} else {
			if normalizedTier == "TIER_1" {
				normalizedTier = "TIER_0"
			}
			if user.Status == string(statemachine.StateKYCPending) {
				if err := s.userFSM.Transition(ctx, user, statemachine.EventKYCRejected); err != nil {
					return err
				}
			} else {
				user.Status = string(statemachine.StateKYCRejected)
			}
			user.KYCTier = normalizedTier
		}
	case "PENDING":
		if isTierUpgrade && currentTier != "TIER_0" {
			user.Status = string(statemachine.StateKYCApproved)
			user.KYCTier = currentTier
		} else {
			user.Status = string(statemachine.StateKYCPending)
			user.KYCTier = currentTier
		}
	default:
		return fmt.Errorf("unsupported KYC status %s", normalizedStatus)
	}

	_, err = s.db.Exec(ctx, `
		UPDATE users
		SET status = $2::user_status,
			kyc_tier = $3::kyc_tier
		WHERE id = $1::uuid
	`, userID, user.Status, user.KYCTier)
	return err
}

func (s *ApplicationService) GetUserKYCTier(ctx context.Context, userID uuid.UUID) (string, error) {
	var tier string
	if err := s.db.QueryRow(ctx, `SELECT kyc_tier::text FROM users WHERE id = $1::uuid`, userID).Scan(&tier); err != nil {
		return "", err
	}
	return tier, nil
}

func normalizeKYCTier(raw string) string {
	value := strings.ToUpper(strings.TrimSpace(raw))
	switch value {
	case "", "TIER_1", "TIER1":
		return "TIER_1"
	case "TIER_2", "TIER2":
		return "TIER_2"
	case "TIER_3", "TIER3":
		return "TIER_3"
	case "TIER_4", "TIER4":
		return "TIER_4"
	default:
		return "TIER_1"
	}
}

func (s *ApplicationService) sumsubLevelNameForTier(tier string) string {
	fallback := func(value string) string {
		if strings.EqualFold(strings.TrimSpace(s.options.Environment), "production") {
			return ""
		}
		return value
	}
	switch normalizeKYCTier(tier) {
	case "TIER_1":
		return firstNonEmptyLocal(s.options.SumsubTier1LevelName, fallback("telegram-tier1"))
	case "TIER_2":
		return firstNonEmptyLocal(s.options.SumsubTier2LevelName, fallback("telegram-tier2"))
	case "TIER_3":
		return firstNonEmptyLocal(s.options.SumsubTier3LevelName, fallback("telegram-tier3"))
	case "TIER_4":
		return firstNonEmptyLocal(s.options.SumsubTier4LevelName, fallback("telegram-tier4"))
	default:
		return firstNonEmptyLocal(s.options.SumsubTier1LevelName, fallback("telegram-tier1"))
	}
}

func (s *ApplicationService) logKYCSubmitStage(stage string, userID string, attrs ...interface{}) {
	if s == nil || s.logger == nil {
		return
	}
	args := []interface{}{
		"stage", stage,
		"user_id", strings.TrimSpace(userID),
		"provider", normalizeKYCProvider(s.options.KYCPrimaryProvider),
	}
	args = append(args, attrs...)
	s.logger.Info(stage, args...)
}

func sumsubLevelEnvName(tier string) string {
	switch normalizeKYCTier(tier) {
	case "TIER_2":
		return "SUMSUB_TIER2_LEVEL_NAME"
	case "TIER_3":
		return "SUMSUB_TIER3_LEVEL_NAME"
	case "TIER_4":
		return "SUMSUB_TIER4_LEVEL_NAME"
	default:
		return "SUMSUB_TIER1_LEVEL_NAME"
	}
}

func normalizeKYCProvider(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "sumsub":
		return "sumsub"
	case "smileid", "smile_id", "smile-id":
		return "smile_id"
	default:
		return "smile_id"
	}
}

func mergeKYCResultMetadata(summary *domain.KYCStatusSummary, result *kyc.KYCResult) {
	if summary == nil || result == nil {
		return
	}
	if strings.TrimSpace(result.ProviderStatus) != "" {
		summary.ProviderStatus = result.ProviderStatus
	}
	if strings.TrimSpace(result.LevelName) != "" {
		summary.LevelName = result.LevelName
	}
	if strings.TrimSpace(result.VerificationURL) != "" {
		summary.VerificationURL = result.VerificationURL
	}
}

func firstNonEmptyLocal(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func kycOutcomeFields(result *kyc.KYCResult) (*bool, *time.Time, string) {
	status := strings.ToUpper(strings.TrimSpace(result.Status))
	switch status {
	case "APPROVED":
		verified := true
		now := time.Now().UTC()
		return &verified, &now, ""
	case "REJECTED":
		verified := false
		return &verified, nil, strings.TrimSpace(result.Reason)
	default:
		return nil, nil, ""
	}
}

func timePointer(value time.Time) *time.Time {
	copyValue := value.UTC()
	return &copyValue
}
