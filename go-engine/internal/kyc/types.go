// internal/kyc/types.go
//
// This file defines all the data types used by the KYC pipeline.
// Keeping types in a separate file makes the orchestrator cleaner
// and makes it easy to find "what data does KYC need?"
package kyc

import (
	"context"

	"github.com/google/uuid"
)

// ──────────────────────────────────────────────
// REQUEST TYPES — What data comes INTO the KYC pipeline
// ──────────────────────────────────────────────

// Tier1KYCRequest contains the data needed for basic KYC verification.
// This is the minimum level that allows a user to trade up to $5,000/month.
type Tier1KYCRequest struct {
	UserID      uuid.UUID `json:"user_id"`
	BVN         string    `json:"bvn"`          // 11-digit Bank Verification Number
	NIN         string    `json:"nin"`          // 11-digit National Identification Number
	FirstName   string    `json:"first_name"`
	LastName    string    `json:"last_name"`
	DateOfBirth string    `json:"date_of_birth"` // Format: YYYY-MM-DD
	PhoneNumber string    `json:"phone_number"`
}

// Tier2KYCRequest adds biometric verification on top of Tier 1.
// Unlocks $5,000 – $20,000/month.
type Tier2KYCRequest struct {
	UserID                uuid.UUID `json:"user_id"`
	SelfieBase64          string    `json:"selfie_base64"`           // Base64-encoded selfie image
	ProofOfAddressBase64  string    `json:"proof_of_address_base64"` // Base64-encoded utility bill, etc.
}

// ──────────────────────────────────────────────
// RESPONSE TYPES — What data comes OUT of the KYC pipeline
// ──────────────────────────────────────────────

// KYCResult is the outcome of a KYC verification attempt.
type KYCResult struct {
	Status string `json:"status"` // "APPROVED" or "REJECTED"
	Tier   string `json:"tier"`   // "TIER_1", "TIER_2", etc.
	Reason string `json:"reason"` // Rejection reason (empty if approved)
}

// ──────────────────────────────────────────────
// REPOSITORY INTERFACE — How the KYC pipeline talks to the database
// ──────────────────────────────────────────────

// KYCRepository defines the database operations that the KYC pipeline needs.
//
// This is an INTERFACE, not a concrete implementation. Why?
//
// 1. TESTABILITY: In unit tests, we can create a "fake" repository that
//    stores data in memory instead of requiring a real database.
//
// 2. DECOUPLING: The KYC pipeline doesn't know (or care) whether data
//    is stored in PostgreSQL, MongoDB, or a text file. It just calls
//    SaveKYCResult() and trusts that it works.
//
// 3. SUBSTITUTABILITY: If you switch databases later, only the repository
//    implementation changes — the KYC orchestrator is untouched.
type KYCRepository interface {
	// SaveKYCResult stores the outcome of a KYC verification.
	// It updates the user's kyc_tier and status in the users table,
	// and creates a record in the kyc_documents table.
	SaveKYCResult(ctx context.Context, userID uuid.UUID, tier string, status string) error

	// GetUserKYCTier retrieves the current KYC tier for a user.
	GetUserKYCTier(ctx context.Context, userID uuid.UUID) (string, error)
}

// ──────────────────────────────────────────────
// VALIDATION HELPERS
// ──────────────────────────────────────────────

// validateNIN checks if a Nigerian National Identification Number is
// in the correct format: exactly 11 digits.
func validateNIN(nin string) error {
	if len(nin) != 11 {
		return &ValidationError{
			Field:   "NIN",
			Message: "must be exactly 11 digits",
			Value:   nin,
		}
	}
	for _, c := range nin {
		if c < '0' || c > '9' {
			return &ValidationError{
				Field:   "NIN",
				Message: "must contain only digits",
				Value:   nin,
			}
		}
	}
	return nil
}

// validateBVN checks if a Bank Verification Number is in the correct format.
func validateBVN(bvn string) error {
	if len(bvn) != 11 {
		return &ValidationError{
			Field:   "BVN",
			Message: "must be exactly 11 digits",
			Value:   bvn,
		}
	}
	for _, c := range bvn {
		if c < '0' || c > '9' {
			return &ValidationError{
				Field:   "BVN",
				Message: "must contain only digits",
				Value:   bvn,
			}
		}
	}
	return nil
}

// ValidationError provides structured validation error information.
type ValidationError struct {
	Field   string
	Message string
	Value   string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}