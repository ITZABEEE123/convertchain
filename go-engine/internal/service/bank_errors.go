package service

import (
	"errors"
	"net"
	"net/http"
	"strings"

	graphclient "convert-chain/go-engine/internal/graph"
)

const (
	bankErrInvalidAccountNumber = "invalid_account_number"
	bankErrInvalidBankCode      = "invalid_bank_code"
	bankErrProviderUnavailable  = "provider_unavailable"
	bankErrAccountNotFound      = "account_not_found"
	bankErrProviderError        = "provider_error"
)

type bankResolveError struct {
	code    string
	message string
	status  int
	details map[string]any
	cause   error
}

func (e *bankResolveError) Error() string {
	if e == nil {
		return ""
	}
	if e.cause != nil {
		return e.code + ": " + e.cause.Error()
	}
	return e.code + ": " + e.message
}

func (e *bankResolveError) Unwrap() error { return e.cause }

func (e *bankResolveError) BankErrorCode() string { return e.code }

func (e *bankResolveError) HTTPStatusCode() int { return e.status }

func (e *bankResolveError) UserMessage() string { return e.message }

func (e *bankResolveError) SafeDetails() map[string]any { return e.details }

func newBankResolveError(code, message string, status int, details map[string]any, cause error) error {
	return &bankResolveError{code: code, message: message, status: status, details: details, cause: cause}
}

func classifyBankResolveError(err error, bankCode, bankName, accountNumber string) error {
	details := map[string]any{
		"bank_code":     strings.TrimSpace(bankCode),
		"bank_name":     strings.TrimSpace(bankName),
		"account_last4": accountLast4(accountNumber),
	}

	var providerErr *graphclient.ProviderError
	if errors.As(err, &providerErr) {
		details["provider_status_code"] = providerErr.StatusCode
		if providerErr.Code != "" {
			details["provider_error_code"] = providerErr.Code
		}
		if providerErr.Message != "" {
			details["provider_error_message"] = providerErr.Message
		}

		switch providerErr.StatusCode {
		case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity:
			return newBankResolveError(
				bankErrAccountNotFound,
				"I could not verify this account. Please check the bank and 10-digit account number.",
				http.StatusBadRequest,
				details,
				err,
			)
		case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return newBankResolveError(
				bankErrProviderUnavailable,
				"Bank verification is temporarily unavailable. Please try again shortly.",
				http.StatusBadGateway,
				details,
				err,
			)
		default:
			return newBankResolveError(
				bankErrProviderError,
				"Bank verification failed. Please try again.",
				http.StatusBadGateway,
				details,
				err,
			)
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return newBankResolveError(
			bankErrProviderUnavailable,
			"Bank verification is temporarily unavailable. Please try again shortly.",
			http.StatusBadGateway,
			details,
			err,
		)
	}

	return newBankResolveError(
		bankErrProviderError,
		"Bank verification failed. Please try again.",
		http.StatusBadGateway,
		details,
		err,
	)
}

func accountLast4(accountNumber string) string {
	value := strings.TrimSpace(accountNumber)
	if len(value) <= 4 {
		return value
	}
	return value[len(value)-4:]
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
