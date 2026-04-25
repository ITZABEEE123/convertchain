package service

import (
	"fmt"
	"os"
	"strings"
)

func buildDepositAddressForTrade(currency string, tradeRef string) (string, error) {
	asset := strings.ToUpper(strings.TrimSpace(currency))
	ref := strings.ToLower(strings.TrimSpace(tradeRef))
	if ref == "" {
		return "", fmt.Errorf("trade reference is required for deposit address allocation")
	}

	if strings.ToLower(strings.TrimSpace(os.Getenv("BLOCKCHAIN_MONITOR_MODE"))) != "production" {
		return fmt.Sprintf("sandbox://deposit/%s/%s", strings.ToLower(asset), ref), nil
	}

	switch asset {
	case "BTC":
		address := firstNonEmptyEnv("BTC_DEPOSIT_ADDRESS", "BTC_PROVIDER_DEPOSIT_ADDRESS")
		if address == "" {
			return "", fmt.Errorf("deposit_address_provider_not_configured: BTC_DEPOSIT_ADDRESS is required in production")
		}
		return address, nil
	case "USDC":
		network := normalizeNetworkName(os.Getenv("USDC_DEPOSIT_NETWORK"))
		if network == "" {
			network = "ethereum"
		}
		address := usdcDepositAddressForNetwork(network)
		if address == "" {
			return "", fmt.Errorf("deposit_address_provider_not_configured: USDC deposit address is required for network %s in production", network)
		}
		return network + ":" + address, nil
	default:
		return "", fmt.Errorf("deposit monitoring is not configured for %s in production", asset)
	}
}

func usdcDepositAddressForNetwork(network string) string {
	switch normalizeNetworkName(network) {
	case "ethereum":
		return firstNonEmptyEnv("USDC_ETH_DEPOSIT_ADDRESS", "USDC_ETHEREUM_DEPOSIT_ADDRESS", "USDC_DEPOSIT_ADDRESS")
	case "polygon":
		return firstNonEmptyEnv("USDC_POLYGON_DEPOSIT_ADDRESS", "USDC_MATIC_DEPOSIT_ADDRESS")
	default:
		return ""
	}
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
