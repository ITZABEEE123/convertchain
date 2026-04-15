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
	ErrCodeKYCRequired       = "KYC_REQUIRED"
	ErrCodeKYCNotApproved    = "KYC_NOT_APPROVED"
	ErrCodeQuoteExpired      = "QUOTE_EXPIRED"
	ErrCodeQuoteUsed         = "QUOTE_ALREADY_USED"
	ErrCodeInsufficientFunds = "INSUFFICIENT_FUNDS"
	ErrCodeRateLimited       = "RATE_LIMITED"
	ErrCodeInternalError     = "INTERNAL_ERROR"
)

func NewError(code, message string, details interface{}) ErrorResponse {
	return ErrorResponse{Error: ErrorBody{Code: code, Message: message, Details: details}}
}