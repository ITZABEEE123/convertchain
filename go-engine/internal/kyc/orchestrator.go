// internal/kyc/orchestrator.go
//
// The KYC Orchestrator coordinates the entire identity verification process.
// It acts as a conductor in an orchestra — it doesn't play any instruments
// itself, but it tells each player (SmileID, Sumsub, database) when and
// what to play.
//
// The orchestrator follows a strict sequence for each tier:
//
//	Tier 1: Validate format → BVN lookup → NIN lookup → Cross-check → Save
//	Tier 2: Liveness check → Proof of address → Save
//	Tier 3: Enhanced due diligence (future implementation)
//	Tier 4: Business KYC (future implementation)
package kyc

import (
	"context"
	"fmt"
	"log/slog"

	"convert-chain/go-engine/internal/kyc/smileid"
	"convert-chain/go-engine/internal/kyc/sumsub"
)

// KYCOrchestrator coordinates KYC verification across multiple providers.
type KYCOrchestrator struct {
	// smileID handles Nigerian identity verification (BVN, NIN lookups).
	// Used for Tier 1 KYC — it connects to NIMC (National Identity
	// Management Commission) databases through the SmileID API.
	smileID *smileid.Client

	// sumsub handles advanced verification (liveness, document OCR).
	// Used for Tier 2+ KYC — it does facial recognition, document
	// authenticity checks, and proof-of-address validation.
	sumsub *sumsub.Client

	// repo is the database interface for saving KYC results.
	// Uses the KYCRepository interface (from types.go) so it can
	// be swapped with a mock in tests.
	repo KYCRepository

	// logger provides structured logging for audit and debugging.
	// slog is Go's standard structured logger (added in Go 1.21).
	logger *slog.Logger
}

// NewKYCOrchestrator creates a new orchestrator with all dependencies injected.
//
// "Dependency injection" means we pass in everything the orchestrator needs
// from the outside, instead of creating them internally. This makes the
// orchestrator testable (we can pass in fake/mock dependencies in tests)
// and flexible (we can swap SmileID for another provider without changing this code).
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

// ──────────────────────────────────────────────
// TIER 1 KYC: BVN + NIN Verification
//
// This is the entry-level KYC that allows users to trade up to $5,000/month.
// It verifies the user's identity by cross-referencing their BVN and NIN
// records through SmileID, which connects to NIMC databases.
//
// The verification flow:
//   1. Validate NIN format (11 digits, numeric)
//   2. Validate BVN format (11 digits, numeric)
//   3. BVN lookup via SmileID → checks name match
//   4. NIN lookup via SmileID → checks NIN validity
//   5. Cross-check: NIN date-of-birth must match BVN date-of-birth
//   6. All checks pass → save result and return APPROVED
// ──────────────────────────────────────────────

func (o *KYCOrchestrator) SubmitTier1KYC(ctx context.Context, req Tier1KYCRequest) (*KYCResult, error) {
	// Structured logging: record that we're starting a Tier 1 KYC attempt.
	// "user_id" is a key-value pair that makes logs searchable.
	// In production, you can filter logs by user_id to see their entire journey.
	o.logger.Info("Starting Tier 1 KYC verification",
		"user_id", req.UserID,
		"phone", req.PhoneNumber, // logged for debugging, encrypted in DB
	)

	// ── Step 1: Validate NIN format ──
	// Before making any external API calls (which cost money and take time),
	// validate locally that the NIN is in the right format.
	if err := validateNIN(req.NIN); err != nil {
		o.logger.Warn("NIN validation failed", "user_id", req.UserID, "error", err)
		return nil, fmt.Errorf("invalid NIN: %w", err)
	}

	// ── Step 2: Validate BVN format ──
	if err := validateBVN(req.BVN); err != nil {
		o.logger.Warn("BVN validation failed", "user_id", req.UserID, "error", err)
		return nil, fmt.Errorf("invalid BVN: %w", err)
	}

	// ── Step 3: BVN lookup via SmileID ──
	// SmileID sends the BVN to the Central Bank of Nigeria's database
	// and returns the name, date of birth, and phone number associated
	// with that BVN. We check if the name matches what the user gave us.
	o.logger.Info("Looking up BVN via SmileID", "user_id", req.UserID)
	bvnResult, err := o.smileID.LookupBVN(ctx, smileid.BVNLookupRequest{
		BVN:         req.BVN,
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		DateOfBirth: req.DateOfBirth,
	})
	if err != nil {
		o.logger.Error("BVN lookup API call failed",
			"user_id", req.UserID,
			"error", err,
		)
		return nil, fmt.Errorf("BVN lookup failed: %w", err)
	}

	// Check if the name on the BVN record matches the name the user provided.
	// SmileID performs fuzzy matching and returns whether it's an "Exact" match.
	if !bvnResult.NameMatch {
		o.logger.Warn("BVN name mismatch",
			"user_id", req.UserID,
			"provided_name", req.FirstName+" "+req.LastName,
			"bvn_name", bvnResult.FullName,
		)
		return &KYCResult{
			Status: "REJECTED",
			Reason: "Name does not match BVN records — please ensure you entered your name exactly as it appears on your bank account",
		}, nil
	}

	// ── Step 4: NIN lookup via SmileID ──
	// Verifies the NIN exists and is valid in the NIMC database.
	o.logger.Info("Looking up NIN via SmileID", "user_id", req.UserID)
	ninResult, err := o.smileID.LookupNIN(ctx, smileid.NINLookupRequest{
		NIN: req.NIN,
	})
	if err != nil {
		o.logger.Error("NIN lookup API call failed",
			"user_id", req.UserID,
			"error", err,
		)
		return nil, fmt.Errorf("NIN lookup failed: %w", err)
	}

	if ninResult.Status != "VALID" {
		o.logger.Warn("NIN invalid or not found", "user_id", req.UserID)
		return &KYCResult{
			Status: "REJECTED",
			Reason: "NIN not found or invalid — please verify your National Identification Number",
		}, nil
	}

	// ── Step 5: Cross-check NIN vs BVN ──
	// The date of birth on the NIN record must match the BVN record.
	// This prevents someone from using a valid BVN and a different
	// person's valid NIN — the DOB must be consistent.
	if ninResult.DateOfBirth != bvnResult.DateOfBirth {
		o.logger.Warn("DOB mismatch between NIN and BVN",
			"user_id", req.UserID,
			"nin_dob", ninResult.DateOfBirth,
			"bvn_dob", bvnResult.DateOfBirth,
		)
		return &KYCResult{
			Status: "REJECTED",
			Reason: "Date of birth mismatch between NIN and BVN records",
		}, nil
	}

	// ── Step 6: All checks passed — save and approve ──
	o.logger.Info("Tier 1 KYC approved", "user_id", req.UserID)
	if err := o.repo.SaveKYCResult(ctx, req.UserID, "TIER_1", "APPROVED"); err != nil {
		o.logger.Error("Failed to save KYC result",
			"user_id", req.UserID,
			"error", err,
		)
		return nil, fmt.Errorf("failed to save KYC result: %w", err)
	}

	return &KYCResult{
		Status: "APPROVED",
		Tier:   "TIER_1",
	}, nil
}

// ──────────────────────────────────────────────
// TIER 2 KYC: Biometric + Proof of Address
//
// Adds liveness verification (to prove the selfie is a real person,
// not a photo of a photo) and proof of address validation.
// Unlocks $5,000 – $20,000/month.
// ──────────────────────────────────────────────

func (o *KYCOrchestrator) SubmitTier2KYC(ctx context.Context, req Tier2KYCRequest) (*KYCResult, error) {
	o.logger.Info("Starting Tier 2 KYC verification", "user_id", req.UserID)

	// ── Step 1: Verify user is already Tier 1 ──
	currentTier, err := o.repo.GetUserKYCTier(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to check current tier: %w", err)
	}
	if currentTier != "TIER_1" {
		return &KYCResult{
			Status: "REJECTED",
			Reason: "Must complete Tier 1 KYC before upgrading to Tier 2",
		}, nil
	}

	// ── Step 2: Liveness check via Sumsub ──
	// The user sends a selfie. Sumsub checks:
	// - Is this a real, live person? (not a printed photo or screen)
	// - Does the face match the photo on their NIN/BVN record?
	o.logger.Info("Running liveness check", "user_id", req.UserID)
	livenessResult, err := o.sumsub.CheckLiveness(ctx, sumsub.LivenessRequest{
		UserID:     req.UserID.String(),
		SelfieData: req.SelfieBase64,
	})
	if err != nil {
		o.logger.Error("Liveness check failed", "user_id", req.UserID, "error", err)
		return nil, fmt.Errorf("liveness check failed: %w", err)
	}
	if !livenessResult.IsLive {
		return &KYCResult{
			Status: "REJECTED",
			Reason: "Liveness check failed — please take a clear selfie in good lighting",
		}, nil
	}

	// ── Step 3: Proof of address verification ──
	// The user uploads a utility bill, bank statement, or similar document.
	// Sumsub uses OCR to extract the address and verify:
	// - The document is genuine (not photoshopped)
	// - It's less than 90 days old
	// - The name on the document matches the user's name
	o.logger.Info("Verifying proof of address", "user_id", req.UserID)
	poaResult, err := o.sumsub.VerifyDocument(ctx, sumsub.DocVerifyRequest{
		UserID:     req.UserID.String(),
		DocType:    "PROOF_OF_ADDRESS",
		DocData:    req.ProofOfAddressBase64,
		MaxAgeDays: 90,
	})
	if err != nil {
		o.logger.Error("PoA verification failed", "user_id", req.UserID, "error", err)
		return nil, fmt.Errorf("proof of address verification failed: %w", err)
	}
	if !poaResult.Verified {
		return &KYCResult{
			Status: "REJECTED",
			Reason: poaResult.RejectReason,
		}, nil
	}

	// ── Step 4: All checks passed ──
	o.logger.Info("Tier 2 KYC approved", "user_id", req.UserID)
	if err := o.repo.SaveKYCResult(ctx, req.UserID, "TIER_2", "APPROVED"); err != nil {
		return nil, fmt.Errorf("failed to save KYC result: %w", err)
	}

	return &KYCResult{
		Status: "APPROVED",
		Tier:   "TIER_2",
	}, nil
}
