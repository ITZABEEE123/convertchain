package dto

type AddBankAccountRequest struct {
	UserID         string `json:"user_id" binding:"required"`
	ProviderBankID string `json:"provider_bank_id"`
	BankCode       string `json:"bank_code" binding:"required,numeric,min=3,max=6"`
	BankName       string `json:"bank_name"`
	AccountNumber  string `json:"account_number" binding:"required,len=10,numeric"`
	AccountName    string `json:"account_name"`
	Currency       string `json:"currency"`
}

type ResolveBankAccountRequest struct {
	UserID         string `json:"user_id" binding:"required"`
	ProviderBankID string `json:"provider_bank_id"`
	BankCode       string `json:"bank_code" binding:"required,numeric,min=3,max=6"`
	BankName       string `json:"bank_name"`
	AccountNumber  string `json:"account_number" binding:"required,len=10,numeric"`
	Currency       string `json:"currency"`
}

type BankDirectoryResponse struct {
	BankID          string `json:"bank_id,omitempty"`
	ProviderBankID  string `json:"provider_bank_id,omitempty"`
	BankCode        string `json:"bank_code"`
	BankName        string `json:"bank_name"`
	Slug            string `json:"slug,omitempty"`
	NIPCode         string `json:"nip_code,omitempty"`
	ShortCode       string `json:"short_code,omitempty"`
	Country         string `json:"country,omitempty"`
	Currency        string `json:"currency,omitempty"`
	ResolveBankCode string `json:"resolve_bank_code,omitempty"`
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
