package domain

// BankDirectoryEntry represents a bank returned by the provider directory.
type BankDirectoryEntry struct {
	BankID          string `json:"bank_id"`
	ProviderBankID  string `json:"provider_bank_id"`
	BankCode        string `json:"bank_code"`
	BankName        string `json:"bank_name"`
	Slug            string `json:"slug"`
	NIPCode         string `json:"nip_code"`
	ShortCode       string `json:"short_code"`
	Country         string `json:"country"`
	Currency        string `json:"currency"`
	ResolveBankCode string `json:"resolve_bank_code"`
}

// BankAccountResolution represents a verified bank account lookup result.
type BankAccountResolution struct {
	BankID        string `json:"bank_id"`
	BankCode      string `json:"bank_code"`
	BankName      string `json:"bank_name"`
	AccountNumber string `json:"account_number"`
	AccountName   string `json:"account_name"`
}
