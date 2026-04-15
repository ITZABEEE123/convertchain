package dto

type AddBankAccountRequest struct {
	UserID        string `json:"user_id" binding:"required"`
	BankCode      string `json:"bank_code" binding:"required,len=3"`
	AccountNumber string `json:"account_number" binding:"required,len=10"`
	AccountName   string `json:"account_name" binding:"required"`
}

type BankAccountResponse struct {
	ID            string `json:"id"`
	UserID        string `json:"user_id"`
	BankCode      string `json:"bank_code"`
	BankName      string `json:"bank_name"`
	AccountNumber string `json:"account_number"` // masked: ******1234
	AccountName   string `json:"account_name"`
	IsVerified    bool   `json:"is_verified"`
	CreatedAt     string `json:"created_at"`
}

type ListBankAccountsResponse struct {
	UserID   string                `json:"user_id"`
	Accounts []BankAccountResponse `json:"accounts"`
}