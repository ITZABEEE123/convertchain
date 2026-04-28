package kyc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"convert-chain/go-engine/internal/kyc/smileid"
	"convert-chain/go-engine/internal/kyc/sumsub"
)

var sensitiveNumberPattern = regexp.MustCompile(`\+?\d[\d\s-]{5,}\d`)

type sumsubProvider interface {
	Enabled() bool
	IsSandbox() bool
	VerifyWebhookSignature(payload []byte, digest, algorithm, secretOverride string) bool
	CreateApplicant(ctx context.Context, req sumsub.ApplicantRequest) (*sumsub.Applicant, error)
	GetApplicantByExternalUserID(ctx context.Context, externalUserID string) (*sumsub.Applicant, error)
	CreateWebSDKLink(ctx context.Context, req sumsub.WebSDKLinkRequest) (*sumsub.WebSDKLink, error)
}

type KYCOrchestrator struct {
	smileID *smileid.Client
	sumsub  sumsubProvider
	repo    KYCRepository
	logger  *slog.Logger
}

func NewKYCOrchestrator(
	smileIDClient *smileid.Client,
	sumsubClient *sumsub.Client,
	repo KYCRepository,
	logger *slog.Logger,
) *KYCOrchestrator {
	return &KYCOrchestrator{
		smileID: smileIDClient,
		sumsub:  sumsubClient,
		repo:    repo,
		logger:  logger,
	}
}

func (o *KYCOrchestrator) SupportsTier1() bool {
	return o != nil && o.smileID != nil && o.smileID.Enabled()
}

func (o *KYCOrchestrator) SupportsTier2() bool {
	return o != nil && o.sumsub != nil && o.sumsub.Enabled()
}

func (o *KYCOrchestrator) SupportsSumsub() bool {
	return o != nil && o.sumsub != nil && o.sumsub.Enabled()
}

func (o *KYCOrchestrator) SumsubSandbox() bool {
	return o != nil && o.sumsub != nil && o.sumsub.IsSandbox()
}

func (o *KYCOrchestrator) VerifySmileIDCallback(signature, timestamp string) bool {
	return o != nil && o.smileID != nil && o.smileID.VerifyCallbackSignature(signature, timestamp)
}

func (o *KYCOrchestrator) VerifySumsubWebhook(payload []byte, digest, algorithm, webhookSecret string) bool {
	return o != nil && o.sumsub != nil && o.sumsub.VerifyWebhookSignature(payload, digest, algorithm, webhookSecret)
}

func (o *KYCOrchestrator) CreateSumsubVerificationLink(ctx context.Context, userID, levelName, email, phone string, ttlInSecs int) (string, error) {
	if !o.SupportsSumsub() {
		return "", fmt.Errorf("sumsub_not_configured")
	}
	link, err := o.sumsub.CreateWebSDKLink(ctx, sumsub.WebSDKLinkRequest{
		UserID:      userID,
		LevelName:   levelName,
		Email:       email,
		PhoneNumber: phone,
		TTLInSecs:   ttlInSecs,
	})
	if err != nil {
		return "", err
	}
	return link.URL, nil
}

func (o *KYCOrchestrator) SubmitTier1KYC(ctx context.Context, req Tier1KYCRequest) (*KYCResult, error) {
	if !o.SupportsTier1() {
		return nil, fmt.Errorf("smile_id_not_configured")
	}

	if err := validateNIN(req.NIN); err != nil {
		return nil, fmt.Errorf("invalid NIN: %w", err)
	}
	if err := validateBVN(req.BVN); err != nil {
		return nil, fmt.Errorf("invalid BVN: %w", err)
	}

	o.logger.Info("running smile id tier1 verification", "user_id", req.UserID)

	bvnResult, err := o.smileID.LookupBVN(ctx, smileid.BVNLookupRequest{
		BVN:         req.BVN,
		FirstName:   req.FirstName,
		MiddleName:  "",
		LastName:    req.LastName,
		DateOfBirth: req.DateOfBirth,
		PhoneNumber: req.PhoneNumber,
	})
	if err != nil {
		return nil, fmt.Errorf("BVN lookup failed: %w", err)
	}
	if !bvnResult.Verified {
		return &KYCResult{
			Status:   "REJECTED",
			Tier:     "TIER_0",
			Provider: "smile_id",
			Reason:   firstNonEmptyReason(bvnResult.Reason, "BVN could not be verified"),
		}, nil
	}
	if !bvnResult.NameMatch {
		return &KYCResult{
			Status:   "REJECTED",
			Tier:     "TIER_0",
			Provider: "smile_id",
			Reason:   "Name does not match BVN records",
		}, nil
	}
	if req.DateOfBirth != "" && !bvnResult.DOBMatch {
		return &KYCResult{
			Status:   "REJECTED",
			Tier:     "TIER_0",
			Provider: "smile_id",
			Reason:   "Date of birth does not match BVN records",
		}, nil
	}

	ninResult, err := o.smileID.LookupNIN(ctx, smileid.NINLookupRequest{
		NIN:         req.NIN,
		FirstName:   req.FirstName,
		MiddleName:  "",
		LastName:    req.LastName,
		DateOfBirth: req.DateOfBirth,
		PhoneNumber: req.PhoneNumber,
	})
	if err != nil {
		return nil, fmt.Errorf("NIN lookup failed: %w", err)
	}
	if ninResult.Status != "VALID" {
		return &KYCResult{
			Status:   "REJECTED",
			Tier:     "TIER_0",
			Provider: "smile_id",
			Reason:   firstNonEmptyReason(ninResult.Reason, "NIN not found or invalid"),
		}, nil
	}
	if !ninResult.NameMatch && ninResult.FullName != "" {
		return &KYCResult{
			Status:   "REJECTED",
			Tier:     "TIER_0",
			Provider: "smile_id",
			Reason:   "Name does not match NIN records",
		}, nil
	}
	if req.DateOfBirth != "" && !ninResult.DOBMatch && ninResult.DateOfBirth != "" {
		return &KYCResult{
			Status:   "REJECTED",
			Tier:     "TIER_0",
			Provider: "smile_id",
			Reason:   "Date of birth does not match NIN records",
		}, nil
	}
	if ninResult.DateOfBirth != "" && bvnResult.DateOfBirth != "" && ninResult.DateOfBirth != bvnResult.DateOfBirth {
		return &KYCResult{
			Status:   "REJECTED",
			Tier:     "TIER_0",
			Provider: "smile_id",
			Reason:   "Date of birth mismatch between NIN and BVN records",
		}, nil
	}

	return &KYCResult{
		Status:   "APPROVED",
		Tier:     "TIER_1",
		Provider: "smile_id",
	}, nil
}

func firstNonEmptyReason(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (o *KYCOrchestrator) SubmitTier2KYC(ctx context.Context, req Tier2KYCRequest) (*KYCResult, error) {
	if !o.SupportsSumsub() {
		return nil, fmt.Errorf("sumsub_not_configured")
	}

	currentTier, err := o.repo.GetUserKYCTier(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to check current tier: %w", err)
	}
	if currentTier == "TIER_0" || currentTier == "" {
		return &KYCResult{
			Status:   "REJECTED",
			Tier:     currentTier,
			Provider: "sumsub",
			Reason:   "Must complete Tier 1 KYC before upgrading",
		}, nil
	}

	targetTier := strings.TrimSpace(strings.ToUpper(req.TargetTier))
	if targetTier == "" {
		targetTier = "TIER_2"
	}

	return o.SubmitSumsubKYC(ctx, SumsubKYCRequest{
		UserID:      req.UserID,
		TargetTier:  targetTier,
		LevelName:   req.LevelName,
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		DateOfBirth: req.DateOfBirth,
		Email:       req.Email,
		PhoneNumber: req.PhoneNumber,
	})
}

func (o *KYCOrchestrator) SubmitSumsubKYC(ctx context.Context, req SumsubKYCRequest) (*KYCResult, error) {
	if !o.SupportsSumsub() {
		return nil, fmt.Errorf("sumsub_not_configured")
	}

	targetTier := strings.TrimSpace(strings.ToUpper(req.TargetTier))
	if targetTier == "" {
		targetTier = "TIER_1"
	}
	levelName := strings.TrimSpace(req.LevelName)
	if levelName == "" {
		return nil, fmt.Errorf("sumsub level name is required")
	}

	externalUserID := req.UserID.String()

	o.logSumsubStage("sumsub_create_applicant_started", externalUserID, levelName)
	applicant, err := o.sumsub.CreateApplicant(ctx, sumsub.ApplicantRequest{
		ExternalUserID: externalUserID,
		LevelName:      levelName,
		FirstName:      req.FirstName,
		LastName:       req.LastName,
		DateOfBirth:    req.DateOfBirth,
		Email:          req.Email,
		PhoneNumber:    req.PhoneNumber,
	})
	duplicateApplicant := false
	if err != nil {
		o.logSumsubFailure("sumsub_create_applicant_failed", externalUserID, levelName, err)
		if !isSumsubDuplicateApplicantError(err) {
			return nil, fmt.Errorf("create sumsub applicant failed: %w", err)
		}

		duplicateApplicant = true
		o.logSumsubStage("sumsub_duplicate_applicant_detected", externalUserID, levelName)
		o.logSumsubStage("sumsub_existing_applicant_fetch_started", externalUserID, levelName)
		applicant, err = o.sumsub.GetApplicantByExternalUserID(ctx, externalUserID)
		if err != nil {
			o.logSumsubFailure("sumsub_existing_applicant_fetch_failed", externalUserID, levelName, err)
			return nil, fmt.Errorf("fetch existing sumsub applicant failed: %w", err)
		}
		o.logSumsubStage("sumsub_existing_applicant_fetch_succeeded", externalUserID, levelName)
	} else {
		o.logSumsubStage("sumsub_create_applicant_succeeded", externalUserID, levelName)
	}

	status, reason := sumsubApplicantKYCStatus(applicant)
	if status == "APPROVED" || status == "REJECTED" {
		if duplicateApplicant {
			o.logSumsubStage("kyc_submit_idempotent_success", externalUserID, levelName)
		}
		resultTier := targetTier
		if status == "REJECTED" {
			resultTier = "TIER_0"
		}
		return &KYCResult{
			Status:         status,
			Tier:           resultTier,
			Provider:       "sumsub",
			ProviderRef:    applicant.ID,
			ProviderStatus: applicant.ReviewStatus,
			LevelName:      levelName,
			Reason:         reason,
		}, nil
	}

	o.logSumsubStage("sumsub_websdk_link_started", externalUserID, levelName)
	link, err := o.sumsub.CreateWebSDKLink(ctx, sumsub.WebSDKLinkRequest{
		UserID:      externalUserID,
		LevelName:   levelName,
		Email:       req.Email,
		PhoneNumber: req.PhoneNumber,
		TTLInSecs:   req.TTLInSecs,
	})
	if err != nil {
		o.logSumsubFailure("sumsub_websdk_link_failed", externalUserID, levelName, err)
		return nil, fmt.Errorf("create sumsub websdk link failed: %w", err)
	}
	o.logSumsubStage("sumsub_websdk_link_succeeded", externalUserID, levelName)
	if duplicateApplicant {
		o.logSumsubStage("kyc_submit_idempotent_success", externalUserID, levelName)
	}

	return &KYCResult{
		Status:          "PENDING",
		Tier:            targetTier,
		Provider:        "sumsub",
		ProviderRef:     applicant.ID,
		ProviderStatus:  applicant.ReviewStatus,
		LevelName:       levelName,
		VerificationURL: link.URL,
	}, nil
}

func isSumsubDuplicateApplicantError(err error) bool {
	var providerErr *sumsub.ProviderError
	if !errors.As(err, &providerErr) {
		return false
	}
	code := strings.ToUpper(strings.TrimSpace(providerErr.Code))
	message := strings.ToLower(strings.TrimSpace(providerErr.Message))
	return providerErr.StatusCode == 409 ||
		strings.Contains(code, "DUPLICATE") ||
		strings.Contains(code, "ALREADY") ||
		strings.Contains(message, "already exists")
}

func sumsubApplicantKYCStatus(applicant *sumsub.Applicant) (string, string) {
	if applicant == nil {
		return "PENDING", ""
	}
	reviewAnswer := strings.ToUpper(strings.TrimSpace(applicant.ReviewResult.ReviewAnswer))
	switch reviewAnswer {
	case "GREEN":
		return "APPROVED", ""
	case "RED":
		return "REJECTED", firstNonEmptyReason(
			applicant.ReviewResult.ModerationComment,
			applicant.ReviewResult.ClientComment,
			"Sumsub verification was rejected",
		)
	}

	switch strings.ToLower(strings.TrimSpace(applicant.ReviewStatus)) {
	case "completed":
		return "APPROVED", ""
	case "rejected":
		return "REJECTED", firstNonEmptyReason(
			applicant.ReviewResult.ModerationComment,
			applicant.ReviewResult.ClientComment,
			"Sumsub verification was rejected",
		)
	default:
		return "PENDING", ""
	}
}

func (o *KYCOrchestrator) logSumsubStage(stage string, userID string, levelName string) {
	if o == nil || o.logger == nil {
		return
	}
	o.logger.Info(
		stage,
		"stage", stage,
		"user_id", userID,
		"provider", "sumsub",
		"level_name", levelName,
	)
}

func (o *KYCOrchestrator) logSumsubFailure(stage string, userID string, levelName string, err error) {
	if o == nil || o.logger == nil {
		return
	}
	args := []interface{}{
		"stage", stage,
		"user_id", userID,
		"provider", "sumsub",
		"level_name", levelName,
	}
	var providerErr *sumsub.ProviderError
	if errors.As(err, &providerErr) {
		args = append(args,
			"provider_status_code", providerErr.StatusCode,
			"provider_error_code", providerErr.Code,
			"provider_error_message", safeProviderErrorMessage(providerErr.Message),
		)
	} else {
		args = append(args, "error", err)
	}
	o.logger.Warn(stage, args...)
}

func safeProviderErrorMessage(message string) string {
	safe := strings.TrimSpace(message)
	if safe == "" {
		return ""
	}
	safe = sensitiveNumberPattern.ReplaceAllString(safe, "<redacted-number>")
	if len(safe) > 240 {
		return safe[:240] + "..."
	}
	return safe
}
