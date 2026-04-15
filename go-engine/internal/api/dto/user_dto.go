package dto

type CreateUserRequest struct {
	TelegramID  int64  `json:"telegram_id" binding:"required"`
	Username    string `json:"username"`
	PhoneNumber string `json:"phone_number"`
	Locale      string `json:"locale"`
}

type CreateUserResponse struct {
	UserID    string `json:"user_id"`
	Status    string `json:"status"`    // "CREATED" or "EXISTING"
	KYCStatus string `json:"kyc_status"` // "NOT_STARTED", "PENDING", "APPROVED", "REJECTED"
}

type ConsentRequest struct {
	UserID         string `json:"user_id" binding:"required"`
	ConsentVersion string `json:"consent_version" binding:"required"`
	ConsentedAt    string `json:"consented_at" binding:"required"` // ISO-8601
}

type ConsentResponse struct {
	UserID         string `json:"user_id"`
	ConsentVersion string `json:"consent_version"`
	Recorded       bool   `json:"recorded"`
}