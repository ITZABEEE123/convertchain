package service

import "testing"

func TestWithSandboxTestBankAddsSyntheticDirectoryEntry(t *testing.T) {
	entries := withSandboxTestBank(defaultBankDirectory())

	found := false
	for _, entry := range entries {
		if entry != nil && entry.BankCode == sandboxTestBankCode {
			found = true
			if entry.BankName != sandboxTestBankName {
				t.Fatalf("expected sandbox bank name %q, got %q", sandboxTestBankName, entry.BankName)
			}
		}
	}

	if !found {
		t.Fatalf("expected sandbox test bank %s to be present", sandboxTestBankCode)
	}
}

func TestResolveSandboxBankAccountSupportsSyntheticBank(t *testing.T) {
	resolution, err := resolveSandboxBankAccount(sandboxTestBankCode, "1234567890")
	if err != nil {
		t.Fatalf("expected sandbox bank resolution to succeed, got error: %v", err)
	}

	if resolution.BankName != sandboxTestBankName {
		t.Fatalf("expected bank name %q, got %q", sandboxTestBankName, resolution.BankName)
	}
	if resolution.AccountName != sandboxVerifiedAccountName {
		t.Fatalf("expected account name %q, got %q", sandboxVerifiedAccountName, resolution.AccountName)
	}
}

func TestResolveSandboxBankAccountSupportsKnownDirectoryBank(t *testing.T) {
	resolution, err := resolveSandboxBankAccount("058", "1234567890")
	if err != nil {
		t.Fatalf("expected known bank resolution to succeed, got error: %v", err)
	}

	if resolution.BankCode != "058" {
		t.Fatalf("expected bank code 058, got %s", resolution.BankCode)
	}
	if resolution.BankName == "" || resolution.BankName == "Nigerian Bank" {
		t.Fatalf("expected a specific bank name, got %q", resolution.BankName)
	}
}

func TestSyntheticSandboxDestinationAndPayoutDetection(t *testing.T) {
	destinationID := makeSandboxDestinationID("058", "1234567890")
	if !isSyntheticSandboxDestinationID(destinationID) {
		t.Fatalf("expected %q to be detected as a synthetic sandbox destination", destinationID)
	}

	payoutID := sandboxSyntheticPayoutPrefix + "12345"
	if !isSyntheticSandboxPayoutID(payoutID) {
		t.Fatalf("expected %q to be detected as a synthetic sandbox payout", payoutID)
	}
}
