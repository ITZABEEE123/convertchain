package service

import (
	"errors"
	"net/http"
	"strings"

	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/kyc/sumsub"
)

type KYCSubmitError struct {
	code       string
	message    string
	statusCode int
	details    map[string]interface{}
	cause      error
}

func newKYCSubmitError(code string, statusCode int, message string, details map[string]interface{}, cause error) *KYCSubmitError {
	return &KYCSubmitError{
		code:       code,
		statusCode: statusCode,
		message:    message,
		details:    details,
		cause:      cause,
	}
}

func (e *KYCSubmitError) Error() string {
	if e == nil {
		return ""
	}
	return e.code
}

func (e *KYCSubmitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *KYCSubmitError) SafeCode() string {
	return e.code
}

func (e *KYCSubmitError) SafeMessage() string {
	return e.message
}

func (e *KYCSubmitError) StatusCode() int {
	return e.statusCode
}

func (e *KYCSubmitError) DetailsMap() map[string]interface{} {
	return e.details
}

func userNotFoundKYCError(cause error) error {
	return newKYCSubmitError(
		dto.ErrCodeUserNotFound,
		http.StatusNotFound,
		"User not found",
		nil,
		cause,
	)
}

func providerConfigKYCError(message string, details map[string]interface{}, cause error) error {
	return newKYCSubmitError(dto.ErrCodeProviderConfiguration, http.StatusBadGateway, message, details, cause)
}

func classifyKYCProviderError(err error) error {
	if err == nil {
		return nil
	}

	var providerErr *sumsub.ProviderError
	if errors.As(err, &providerErr) {
		details := map[string]interface{}{
			"provider":  "sumsub",
			"operation": providerErr.Operation,
		}
		if providerErr.StatusCode > 0 {
			details["provider_status_code"] = providerErr.StatusCode
		}
		if strings.TrimSpace(providerErr.Code) != "" {
			details["provider_error_code"] = providerErr.Code
		}

		code := strings.ToUpper(strings.TrimSpace(providerErr.Code))
		message := strings.ToLower(strings.TrimSpace(providerErr.Message))
		switch {
		case code == "SUMSUB_NOT_CONFIGURED" || code == "SUMSUB_LEVEL_NAME_MISSING":
			return providerConfigKYCError("KYC provider is not configured", details, err)
		case providerErr.StatusCode == http.StatusUnauthorized ||
			providerErr.StatusCode == http.StatusForbidden ||
			strings.Contains(code, "AUTH") ||
			strings.Contains(code, "SIGNATURE") ||
			strings.Contains(message, "signature") ||
			strings.Contains(message, "unauthorized") ||
			strings.Contains(message, "forbidden"):
			return newKYCSubmitError(
				dto.ErrCodeProviderAuthFailed,
				http.StatusBadGateway,
				"KYC provider authentication failed",
				details,
				err,
			)
		case providerErr.StatusCode == http.StatusConflict ||
			strings.Contains(code, "DUPLICATE") ||
			strings.Contains(code, "ALREADY") ||
			strings.Contains(message, "already exists"):
			return newKYCSubmitError(
				dto.ErrCodeDuplicateKYCSubmission,
				http.StatusConflict,
				"KYC submission already exists",
				details,
				err,
			)
		default:
			return newKYCSubmitError(
				dto.ErrCodeProviderError,
				http.StatusBadGateway,
				"KYC provider is temporarily unavailable",
				details,
				err,
			)
		}
	}

	normalized := strings.ToLower(err.Error())
	if strings.Contains(normalized, "not_configured") || strings.Contains(normalized, "level name") {
		return providerConfigKYCError(
			"KYC provider is not configured",
			map[string]interface{}{"provider": "sumsub"},
			err,
		)
	}

	return newKYCSubmitError(
		dto.ErrCodeProviderError,
		http.StatusBadGateway,
		"KYC provider is temporarily unavailable",
		map[string]interface{}{"provider": "sumsub"},
		err,
	)
}
