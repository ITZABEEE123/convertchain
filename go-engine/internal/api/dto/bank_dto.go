package dto

type AddBankAccountRequest struct {
	UserID        string `json:"user_id" binding:"required"`
	BankCode      string `json:"bank_code" binding:"required,numeric,min=3,max=6"`
	AccountNumber string `json:"account_number" binding:"required,len=10,numeric"`
	AccountName   string `json:"account_name"`
}

type ResolveBankAccountRequest struct {
	UserID        string `json:"user_id" binding:"required"`
	BankCode      string `json:"bank_code" binding:"required,numeric,min=3,max=6"`
	AccountNumber string `json:"account_number" binding:"required,len=10,numeric"`
}

type BankDirectoryResponse struct {
	BankID   string `json:"bank_id,omitempty"`
	BankCode string `json:"bank_code"`
	BankName string `json:"bank_name"`
}

type ListBanksResponse struct {
	Banks []BankDirectoryResponse `json:"banks"`
}

type ResolveBankAccountResponse struct {
	BankID        string `json:"bank_id,omitempty"`
	BankCode      string `json:"bank_code"`
	BankName      string `json:"bank_name"`
	AccountNumber string `json:"account_number"`
	AccountName   string `json:"account_name"`
}

type BankAccountResponse struct {
	BankAccountID string `json:"bank_account_id"`
	UserID        string `json:"user_id"`
	BankCode      string `json:"bank_code"`
	BankName      string `json:"bank_name"`
	AccountNumber string `json:"account_number"`
	AccountName   string `json:"account_name"`
	IsVerified    bool   `json:"is_verified"`
	IsPrimary     bool   `json:"is_primary"`
	CreatedAt     string `json:"created_at"`
}

type ListBankAccountsResponse struct {
	UserID   string                `json:"user_id"`
	Accounts []BankAccountResponse `json:"accounts"`
}
