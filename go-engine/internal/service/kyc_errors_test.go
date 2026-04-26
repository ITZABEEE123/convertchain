package service

import (
	"net/http"
	"testing"

	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/kyc/sumsub"
)

func TestClassifyKYCProviderAuthFailure(t *testing.T) {
	err := classifyKYCProviderError(&sumsub.ProviderError{
		Operation:  "create_applicant",
		Code:       "SUMSUB_HTTP_401",
		Message:    "Unauthorized",
		StatusCode: http.StatusUnauthorized,
	})

	submitErr, ok := err.(*KYCSubmitError)
	if !ok {
		t.Fatalf("expected KYCSubmitError, got %T", err)
	}
	if submitErr.SafeCode() != dto.ErrCodeProviderAuthFailed {
		t.Fatalf("expected PROVIDER_AUTH_FAILED, got %s", submitErr.SafeCode())
	}
	if submitErr.StatusCode() != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", submitErr.StatusCode())
	}
}

func TestClassifyKYCProviderDuplicate(t *testing.T) {
	err := classifyKYCProviderError(&sumsub.ProviderError{
		Operation:  "create_applicant",
		Code:       "APPLICANT_ALREADY_EXISTS",
		Message:    "Applicant already exists",
		StatusCode: http.StatusConflict,
	})

	submitErr, ok := err.(*KYCSubmitError)
	if !ok {
		t.Fatalf("expected KYCSubmitError, got %T", err)
	}
	if submitErr.SafeCode() != dto.ErrCodeDuplicateKYCSubmission {
		t.Fatalf("expected DUPLICATE_KYC_SUBMISSION, got %s", submitErr.SafeCode())
	}
	if submitErr.StatusCode() != http.StatusConflict {
		t.Fatalf("expected 409, got %d", submitErr.StatusCode())
	}
}
