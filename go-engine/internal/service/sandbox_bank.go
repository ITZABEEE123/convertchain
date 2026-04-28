package service

import (
	"fmt"
	"strings"

	"convert-chain/go-engine/internal/domain"
)

const (
	sandboxTestBankCode          = "000000"
	sandboxTestBankID            = "SANDBOX-000000"
	sandboxTestBankName          = "Sandbox Test Bank"
	sandboxVerifiedAccountName   = "Sandbox Test Account"
	sandboxDestinationIDPrefix   = "sandbox://payout-destination/"
	sandboxSyntheticPayoutPrefix = "sandbox-local-payout-"
)

func withSandboxTestBank(entries []*domain.BankDirectoryEntry) []*domain.BankDirectoryEntry {
	return mergeBankDirectories([]*domain.BankDirectoryEntry{sandboxTestBankEntry()}, entries)
}

func sandboxTestBankEntry() *domain.BankDirectoryEntry {
	return &domain.BankDirectoryEntry{
		BankID:          sandboxTestBankID,
		ProviderBankID:  sandboxTestBankID,
		BankCode:        sandboxTestBankCode,
		BankName:        sandboxTestBankName,
		Country:         "NG",
		Currency:        "NGN",
		ResolveBankCode: sandboxTestBankCode,
	}
}

func resolveSandboxBankAccount(bankCode, accountNumber string) (*domain.BankAccountResolution, error) {
	normalizedCode := strings.TrimSpace(bankCode)
	normalizedAccount := strings.TrimSpace(accountNumber)
	if normalizedCode == "" {
		return nil, fmt.Errorf("bank code is required")
	}
	if normalizedAccount == "" {
		return nil, fmt.Errorf("account number is required")
	}

	entry := lookupBankByCode(normalizedCode)
	if entry == nil && normalizedCode != sandboxTestBankCode {
		return nil, fmt.Errorf("sandbox bank code %s is not supported", normalizedCode)
	}

	bankID := sandboxSyntheticBankID(normalizedCode, entry)
	bankName := sandboxSyntheticBankName(normalizedCode, entry)

	return &domain.BankAccountResolution{
		BankID:        bankID,
		BankCode:      normalizedCode,
		BankName:      bankName,
		AccountNumber: normalizedAccount,
		AccountName:   sandboxVerifiedAccountName,
	}, nil
}

func sandboxSyntheticBankID(bankCode string, entry *domain.BankDirectoryEntry) string {
	if normalized := strings.TrimSpace(bankCode); normalized == sandboxTestBankCode {
		return sandboxTestBankID
	}
	if entry != nil && strings.TrimSpace(entry.BankID) != "" {
		return "SANDBOX-" + strings.TrimSpace(entry.BankID)
	}
	return "SANDBOX-" + strings.TrimSpace(bankCode)
}

func sandboxSyntheticBankName(bankCode string, entry *domain.BankDirectoryEntry) string {
	if strings.TrimSpace(bankCode) == sandboxTestBankCode {
		return sandboxTestBankName
	}
	if entry != nil && strings.TrimSpace(entry.BankName) != "" {
		return strings.TrimSpace(entry.BankName)
	}
	return bankNameFromCode(bankCode)
}

func makeSandboxDestinationID(bankCode, accountNumber string) string {
	return fmt.Sprintf("%s%s/%s", sandboxDestinationIDPrefix, strings.TrimSpace(bankCode), strings.TrimSpace(accountNumber))
}

func isSyntheticSandboxDestinationID(destinationID string) bool {
	return strings.HasPrefix(strings.TrimSpace(destinationID), sandboxDestinationIDPrefix)
}

func isSyntheticSandboxPayoutID(payoutID string) bool {
	return strings.HasPrefix(strings.TrimSpace(payoutID), sandboxSyntheticPayoutPrefix)
}
