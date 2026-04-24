package dto

type SetupTransactionPasswordRequest struct {
	UserID               string `json:"user_id" binding:"required"`
	TransactionPassword  string `json:"transaction_password" binding:"required"`
	ConfirmPassword      string `json:"confirm_password" binding:"required"`
}

type SetupTransactionPasswordResponse struct {
	UserID                 string `json:"user_id"`
	TransactionPasswordSet bool   `json:"transaction_password_set"`
	SetAt                  string `json:"set_at"`
}

type DeleteAccountRequest struct {
	UserID              string `json:"user_id" binding:"required"`
	ConfirmationText    string `json:"confirmation_text" binding:"required"`
	TransactionPassword string `json:"transaction_password" binding:"required"`
}

type DeleteAccountQuotaResponse struct {
	UserID             string `json:"user_id"`
	RemainingDeletions int    `json:"remaining_deletions"`
	WindowDays         int    `json:"window_days"`
}

type DeleteAccountResponse struct {
	UserID    string `json:"user_id"`
	Deleted   bool   `json:"deleted"`
	DeletedAt string `json:"deleted_at"`
}
