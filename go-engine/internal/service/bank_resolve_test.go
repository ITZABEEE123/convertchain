package service

import (
	"context"
	"testing"
	"time"

	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/domain"
	graphclient "convert-chain/go-engine/internal/graph"
)

func TestLookupProviderBankForResolveMapsShortCodeToNIPCode(t *testing.T) {
	svc := &ApplicationService{
		graph: graphclient.NewClient("test-key", false),
		bankDirectoryCache: []*domain.BankDirectoryEntry{
			{
				BankID:          "bk-zenith",
				ProviderBankID:  "bk-zenith",
				BankCode:        "000015",
				BankName:        "ZENITH BANK PLC",
				Slug:            "zenith",
				NIPCode:         "000015",
				ShortCode:       "057",
				Currency:        "NGN",
				ResolveBankCode: "000015",
			},
		},
		bankDirectoryCacheExpiresAt: time.Now().Add(time.Hour),
	}

	bank := svc.lookupProviderBankForResolve(context.Background(), dto.ResolveBankAccountRequest{
		BankName: "ZENITH BANK PLC",
		BankCode: "057",
	})
	if bank == nil {
		t.Fatal("expected bank match")
	}
	if bank.ResolveBankCode != "000015" {
		t.Fatalf("expected nip resolve code 000015, got %q", bank.ResolveBankCode)
	}
}
