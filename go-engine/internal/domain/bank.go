package domain

// BankDirectoryEntry represents a bank returned by the provider directory.
type BankDirectoryEntry struct {
	BankID   string `json:"bank_id"`
	BankCode string `json:"bank_code"`
	BankName string `json:"bank_name"`
}

// BankAccountResolution represents a verified bank account lookup result.
type BankAccountResolution struct {
	BankID        string `json:"bank_id"`
	BankCode      string `json:"bank_code"`
	BankName      string `json:"bank_name"`
	AccountNumber string `json:"account_number"`
	AccountName   string `json:"account_name"`
}
