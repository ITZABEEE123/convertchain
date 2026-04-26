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
	if err == nil || !strings.Contains(err.Error(), "deposit_address_provider_not_configured") {
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

func TestBuildDepositAddressForTradeTagsUSDTNetworkAddress(t *testing.T) {
	t.Setenv("BLOCKCHAIN_MONITOR_MODE", "production")
	t.Setenv("USDT_DEPOSIT_NETWORK", "bsc")
	t.Setenv("USDT_BSC_DEPOSIT_ADDRESS", "0xbsc")

	address, err := buildDepositAddressForTrade("USDT", "TRD-ABC123")
	if err != nil {
		t.Fatalf("build deposit address: %v", err)
	}
	if address != "bsc:0xbsc" {
		t.Fatalf("unexpected USDT address: %s", address)
	}
}

func TestBuildDepositAddressForTradeTagsNativeEVMAssets(t *testing.T) {
	t.Setenv("BLOCKCHAIN_MONITOR_MODE", "production")
	t.Setenv("ETH_DEPOSIT_ADDRESS", "0xeth")
	t.Setenv("BNB_BSC_DEPOSIT_ADDRESS", "0xbnb")

	ethAddress, err := buildDepositAddressForTrade("ETH", "TRD-ABC123")
	if err != nil {
		t.Fatalf("build ETH deposit address: %v", err)
	}
	if ethAddress != "ethereum:0xeth" {
		t.Fatalf("unexpected ETH address: %s", ethAddress)
	}

	bnbAddress, err := buildDepositAddressForTrade("BNB", "TRD-ABC123")
	if err != nil {
		t.Fatalf("build BNB deposit address: %v", err)
	}
	if bnbAddress != "bsc:0xbnb" {
		t.Fatalf("unexpected BNB address: %s", bnbAddress)
	}
}

func TestBuildDepositAddressForTradeRejectsUnsupportedProductionAsset(t *testing.T) {
	t.Setenv("BLOCKCHAIN_MONITOR_MODE", "production")

	_, err := buildDepositAddressForTrade("DOGE", "TRD-ABC123")
	if err == nil || !strings.Contains(err.Error(), "not configured for DOGE") {
		t.Fatalf("expected unsupported asset error, got %v", err)
	}
}
