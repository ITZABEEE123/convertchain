package kyc

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"convert-chain/go-engine/internal/kyc/smileid"
	"convert-chain/go-engine/internal/kyc/sumsub"
)

type KYCOrchestrator struct {
	smileID *smileid.Client
	sumsub  *sumsub.Client
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

func (o *KYCOrchestrator) VerifySmileIDCallback(signature, timestamp string) bool {
	return o != nil && o.smileID != nil && o.smileID.VerifyCallbackSignature(signature, timestamp)
}

func (o *KYCOrchestrator) VerifySumsubWebhook(payload []byte, digest, algorithm, webhookSecret string) bool {
	return o != nil && o.sumsub != nil && o.sumsub.VerifyWebhookSignature(payload, digest, algorithm, webhookSecret)
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
	if !o.SupportsTier2() {
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

	applicant, err := o.sumsub.CreateApplicant(ctx, sumsub.ApplicantRequest{
		ExternalUserID: req.UserID.String(),
		LevelName:      req.LevelName,
		FirstName:      req.FirstName,
		LastName:       req.LastName,
		DateOfBirth:    req.DateOfBirth,
		Email:          req.Email,
		PhoneNumber:    req.PhoneNumber,
	})
	if err != nil {
		return nil, fmt.Errorf("create sumsub applicant failed: %w", err)
	}

	return &KYCResult{
		Status:      "PENDING",
		Tier:        targetTier,
		Provider:    "sumsub",
		ProviderRef: applicant.ID,
	}, nil
}
