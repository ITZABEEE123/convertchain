package service

import (
	"context"
	"errors"
	"fmt"
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
	user, err := s.getUserByID(ctx, req.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errors.New("user_not_found")
		}
		return nil, err
	}

	tier := normalizeKYCTier(req.Tier)
	if tier == "TIER_1" {
		return s.submitTier1KYC(ctx, user, req)
	}
	return s.submitTier2PlusKYC(ctx, user, req, tier)
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

	if err := s.persistTier1KYCOutcome(ctx, &workingCopy, req, phone, result); err != nil {
		return nil, err
	}

	return s.buildKYCStatusSummary(ctx, req.UserID)
}

func (s *ApplicationService) submitTier2PlusKYC(ctx context.Context, user *domain.User, req dto.KYCSubmitRequest, tier string) (*domain.KYCStatusSummary, error) {
	if user.Status != string(statemachine.StateKYCApproved) {
		return nil, errors.New("tier_upgrade_requires_approved_kyc")
	}

	if s.kycOrchestrator == nil || !s.kycOrchestrator.SupportsTier2() {
		return nil, errors.New("kyc_provider_not_configured")
	}

	result, err := s.kycOrchestrator.SubmitTier2KYC(ctx, kyc.Tier2KYCRequest{
		UserID:               user.ID,
		TargetTier:           tier,
		LevelName:            sumsubLevelNameForTier(tier),
		FirstName:            req.FirstName,
		LastName:             req.LastName,
		DateOfBirth:          req.DateOfBirth,
		PhoneNumber:          normalizePhoneNumber(req.PhoneNumber),
		SelfieBase64:         req.SelfieBase64,
		ProofOfAddressBase64: req.ProofOfAddressBase64,
	})
	if err != nil {
		return nil, err
	}

	if err := s.persistAdvancedKYCSubmission(ctx, user.ID, req, result); err != nil {
		return nil, err
	}

	return &domain.KYCStatusSummary{
		UserID:      user.ID,
		Status:      strings.ToUpper(strings.TrimSpace(result.Status)),
		Tier:        tier,
		Provider:    result.Provider,
		ProviderRef: result.ProviderRef,
		SubmittedAt: timePointer(time.Now().UTC()),
		CompletedAt: nil,
	}, nil
}

func (s *ApplicationService) runTier1Provider(ctx context.Context, req dto.KYCSubmitRequest, phone string) (*kyc.KYCResult, error) {
	if s.kycOrchestrator != nil && s.kycOrchestrator.SupportsTier1() {
		return s.kycOrchestrator.SubmitTier1KYC(ctx, kyc.Tier1KYCRequest{
			UserID:      uuid.MustParse(req.UserID),
			BVN:         req.BVN,
			NIN:         req.NIN,
			FirstName:   req.FirstName,
			LastName:    req.LastName,
			DateOfBirth: req.DateOfBirth,
			PhoneNumber: phone,
		})
	}

	if s.options.AutoApproveKYC {
		return &kyc.KYCResult{Status: "APPROVED", Tier: "TIER_1", Provider: "mock-local"}, nil
	}

	return nil, errors.New("kyc_provider_not_configured")
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
		UserID: user.ID,
		Status: userStatusToAPI(user.Status),
		Tier:   user.KYCTier,
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
	if record.Verified == nil && user.Status == string(statemachine.StateKYCApproved) && summary.Provider == "sumsub" {
		summary.Status = "PENDING"
	}
	return summary, nil
}

func (s *ApplicationService) SaveKYCResult(ctx context.Context, userID uuid.UUID, tier string, status string) error {
	user, err := s.getUserByID(ctx, userID.String())
	if err != nil {
		return err
	}

	normalizedStatus := strings.ToUpper(strings.TrimSpace(status))
	normalizedTier := normalizeKYCTier(tier)
	if normalizedTier == "TIER_1" && normalizedStatus == "REJECTED" {
		normalizedTier = "TIER_0"
	}

	switch normalizedStatus {
	case "APPROVED":
		if user.Status == string(statemachine.StateKYCPending) {
			if err := s.userFSM.Transition(ctx, user, statemachine.EventKYCApproved); err != nil {
				return err
			}
		} else {
			user.Status = string(statemachine.StateKYCApproved)
		}
		user.KYCTier = normalizedTier
	case "REJECTED":
		if user.Status == string(statemachine.StateKYCPending) {
			if err := s.userFSM.Transition(ctx, user, statemachine.EventKYCRejected); err != nil {
				return err
			}
		} else {
			user.Status = string(statemachine.StateKYCRejected)
		}
		user.KYCTier = normalizedTier
	case "PENDING":
		user.Status = string(statemachine.StateKYCPending)
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

func sumsubLevelNameForTier(tier string) string {
	switch normalizeKYCTier(tier) {
	case "TIER_3":
		return "telegram-tier3"
	case "TIER_4":
		return "telegram-tier4"
	default:
		return "telegram-tier2"
	}
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
