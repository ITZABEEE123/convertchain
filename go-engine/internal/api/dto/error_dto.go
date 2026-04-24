package dto

// ErrorResponse is the standard error envelope returned by all endpoints.
// ALL error responses from the Go engine have this shape.
//
// Example:
//   { "error": { "code": "QUOTE_EXPIRED", "message": "...", "details": null } }
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

// Standard error codes.
const (
	ErrCodeUnauthorized      = "UNAUTHORIZED"
	ErrCodeNotFound          = "NOT_FOUND"
	ErrCodeValidation        = "VALIDATION_ERROR"
	ErrCodeConflict          = "CONFLICT"
	ErrCodeKYCRequired       = "KYC_REQUIRED"
	ErrCodeKYCNotApproved    = "KYC_NOT_APPROVED"
	ErrCodeQuoteExpired      = "QUOTE_EXPIRED"
	ErrCodeQuoteUsed         = "QUOTE_ALREADY_USED"
	ErrCodeInsufficientFunds = "INSUFFICIENT_FUNDS"
	ErrCodeRateLimited       = "RATE_LIMITED"
	ErrCodeInternalError     = "INTERNAL_ERROR"
	ErrCodeTradePreflightFailed = "TRADE_PREFLIGHT_FAILED"
	ErrCodeNotificationClaimConflict = "NOTIFICATION_CLAIM_CONFLICT"
	ErrCodeTxnPasswordInvalid = "TRANSACTION_PASSWORD_INVALID"
	ErrCodeTxnPasswordLocked  = "TRANSACTION_PASSWORD_LOCKED"
	ErrCodeTxnPasswordMissing = "TRANSACTION_PASSWORD_NOT_SET"
	ErrCodeDeletionBlocked    = "ACCOUNT_DELETION_BLOCKED"
	ErrCodeDeletionQuota      = "ACCOUNT_DELETION_QUOTA_EXCEEDED"
)

func NewError(code, message string, details interface{}) ErrorResponse {
	return ErrorResponse{Error: ErrorBody{Code: code, Message: message, Details: details}}
}
