package service

import (
	"strings"
	"testing"
)

func TestBuildDepositAddressForTradeUsesSandboxOutsideProduction(t *testing.T) {
	t.Setenv("BLOCKCHAIN_MONITOR_MODE", "sandbox")

	address, err := buildDepositAddressForTrade("BTC", "TRD-ABC123")
	if err != nil {
		t.Fatalf("build deposit address: %v", err)
	}

	if address != "sandbox://deposit/btc/trd-abc123" {
		t.Fatalf("unexpected sandbox address: %s", address)
	}
}

func TestBuildDepositAddressForTradeRequiresBTCProviderAddressInProduction(t *testing.T) {
	t.Setenv("BLOCKCHAIN_MONITOR_MODE", "production")

	_, err := buildDepositAddressForTrade("BTC", "TRD-ABC123")
	if err == nil || !strings.Contains(err.Error(), "BTC_DEPOSIT_ADDRESS") {
		t.Fatalf("expected missing BTC address error, got %v", err)
	}
}

func TestBuildDepositAddressForTradeUsesProductionBTCProviderAddress(t *testing.T) {
	t.Setenv("BLOCKCHAIN_MONITOR_MODE", "production")
	t.Setenv("BTC_DEPOSIT_ADDRESS", "bc1qexample")

	address, err := buildDepositAddressForTrade("BTC", "TRD-ABC123")
	if err != nil {
		t.Fatalf("build deposit address: %v", err)
	}
	if address != "bc1qexample" {
		t.Fatalf("unexpected BTC address: %s", address)
	}
}

func TestBuildDepositAddressForTradeTagsUSDCNetworkAddress(t *testing.T) {
	t.Setenv("BLOCKCHAIN_MONITOR_MODE", "production")
	t.Setenv("USDC_DEPOSIT_NETWORK", "polygon")
	t.Setenv("USDC_POLYGON_DEPOSIT_ADDRESS", "0xabc")

	address, err := buildDepositAddressForTrade("USDC", "TRD-ABC123")
	if err != nil {
		t.Fatalf("build deposit address: %v", err)
	}
	if address != "polygon:0xabc" {
		t.Fatalf("unexpected USDC address: %s", address)
	}
}

func TestBuildDepositAddressForTradeRejectsUnsupportedProductionAsset(t *testing.T) {
	t.Setenv("BLOCKCHAIN_MONITOR_MODE", "production")

	_, err := buildDepositAddressForTrade("USDT", "TRD-ABC123")
	if err == nil || !strings.Contains(err.Error(), "not configured for USDT") {
		t.Fatalf("expected unsupported asset error, got %v", err)
	}
}
