package kyc

import (
	"context"

	"github.com/google/uuid"
)

type Tier1KYCRequest struct {
	UserID      uuid.UUID `json:"user_id"`
	BVN         string    `json:"bvn"`
	NIN         string    `json:"nin"`
	FirstName   string    `json:"first_name"`
	LastName    string    `json:"last_name"`
	DateOfBirth string    `json:"date_of_birth"`
	PhoneNumber string    `json:"phone_number"`
}

type Tier2KYCRequest struct {
	UserID               uuid.UUID `json:"user_id"`
	TargetTier           string    `json:"target_tier"`
	LevelName            string    `json:"level_name"`
	FirstName            string    `json:"first_name"`
	LastName             string    `json:"last_name"`
	DateOfBirth          string    `json:"date_of_birth"`
	Email                string    `json:"email"`
	PhoneNumber          string    `json:"phone_number"`
	SelfieBase64         string    `json:"selfie_base64"`
	ProofOfAddressBase64 string    `json:"proof_of_address_base64"`
}

type KYCResult struct {
	Status      string `json:"status"`
	Tier        string `json:"tier"`
	Reason      string `json:"reason"`
	Provider    string `json:"provider"`
	ProviderRef string `json:"provider_ref"`
}

type KYCRepository interface {
	SaveKYCResult(ctx context.Context, userID uuid.UUID, tier string, status string) error
	GetUserKYCTier(ctx context.Context, userID uuid.UUID) (string, error)
}

func validateNIN(nin string) error {
	if len(nin) != 11 {
		return &ValidationError{Field: "NIN", Message: "must be exactly 11 digits", Value: nin}
	}
	for _, c := range nin {
		if c < '0' || c > '9' {
			return &ValidationError{Field: "NIN", Message: "must contain only digits", Value: nin}
		}
	}
	return nil
}

func validateBVN(bvn string) error {
	if len(bvn) != 11 {
		return &ValidationError{Field: "BVN", Message: "must be exactly 11 digits", Value: bvn}
	}
	for _, c := range bvn {
		if c < '0' || c > '9' {
			return &ValidationError{Field: "BVN", Message: "must contain only digits", Value: bvn}
		}
	}
	return nil
}

type ValidationError struct {
	Field   string
	Message string
	Value   string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}
