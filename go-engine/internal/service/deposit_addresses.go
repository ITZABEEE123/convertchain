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
		return requiredDepositAddress(asset, "btc", "BTC_DEPOSIT_ADDRESS", "BTC_PROVIDER_DEPOSIT_ADDRESS", "BINANCE_BTC_DEPOSIT_ADDRESS", "BYBIT_BTC_DEPOSIT_ADDRESS")
	case "ETH":
		network := normalizeNetworkName(firstNonEmptyEnv("ETH_DEPOSIT_NETWORK", "ETH_NETWORK"))
		if network == "" || network == "default" {
			network = "ethereum"
		}
		return requiredTaggedDepositAddress(asset, network, evmNativeDepositAddressForNetwork("ETH", network))
	case "BNB":
		network := normalizeNetworkName(firstNonEmptyEnv("BNB_DEPOSIT_NETWORK", "BNB_NETWORK"))
		if network == "" || network == "default" {
			network = "bsc"
		}
		return requiredTaggedDepositAddress(asset, network, evmNativeDepositAddressForNetwork("BNB", network))
	case "USDC", "USDT":
		network := normalizeNetworkName(os.Getenv(asset + "_DEPOSIT_NETWORK"))
		if network == "" || network == "default" {
			network = "ethereum"
		}
		return requiredTaggedDepositAddress(asset, network, stablecoinDepositAddressForNetwork(asset, network))
	default:
		return "", fmt.Errorf("deposit monitoring is not configured for %s in production", asset)
	}
}

func requiredDepositAddress(asset string, network string, keys ...string) (string, error) {
	address := firstNonEmptyEnv(keys...)
	if address == "" {
		return "", fmt.Errorf("deposit_address_provider_not_configured: %s deposit address is required for network %s in production", asset, network)
	}
	return address, nil
}

func requiredTaggedDepositAddress(asset string, network string, address string) (string, error) {
	if address == "" {
		return "", fmt.Errorf("deposit_address_provider_not_configured: %s deposit address is required for network %s in production", asset, network)
	}
	return network + ":" + address, nil
}

func evmNativeDepositAddressForNetwork(asset, network string) string {
	switch strings.ToUpper(strings.TrimSpace(asset)) {
	case "ETH":
		if normalizeNetworkName(network) == "ethereum" {
			return firstNonEmptyEnv("ETH_ETH_DEPOSIT_ADDRESS", "ETH_ETHEREUM_DEPOSIT_ADDRESS", "ETH_DEPOSIT_ADDRESS", "BINANCE_ETH_DEPOSIT_ADDRESS", "BYBIT_ETH_DEPOSIT_ADDRESS")
		}
	case "BNB":
		if normalizeNetworkName(network) == "bsc" {
			return firstNonEmptyEnv("BNB_BSC_DEPOSIT_ADDRESS", "BNB_BEP20_DEPOSIT_ADDRESS", "BNB_DEPOSIT_ADDRESS", "BINANCE_BNB_DEPOSIT_ADDRESS", "BYBIT_BNB_DEPOSIT_ADDRESS")
		}
	}
	return ""
}

func stablecoinDepositAddressForNetwork(asset, network string) string {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	switch normalizeNetworkName(network) {
	case "ethereum":
		return firstNonEmptyEnv(
			asset+"_ETH_DEPOSIT_ADDRESS",
			asset+"_ETHEREUM_DEPOSIT_ADDRESS",
			"GRAPH_"+asset+"_ETH_DEPOSIT_ADDRESS",
			"GRAPH_"+asset+"_ETHEREUM_DEPOSIT_ADDRESS",
			"BINANCE_"+asset+"_ETH_DEPOSIT_ADDRESS",
			"BYBIT_"+asset+"_ETH_DEPOSIT_ADDRESS",
			asset+"_DEPOSIT_ADDRESS",
		)
	case "polygon":
		return firstNonEmptyEnv(
			asset+"_POLYGON_DEPOSIT_ADDRESS",
			asset+"_MATIC_DEPOSIT_ADDRESS",
			"GRAPH_"+asset+"_POLYGON_DEPOSIT_ADDRESS",
			"BINANCE_"+asset+"_POLYGON_DEPOSIT_ADDRESS",
			"BYBIT_"+asset+"_POLYGON_DEPOSIT_ADDRESS",
		)
	case "bsc":
		return firstNonEmptyEnv(
			asset+"_BSC_DEPOSIT_ADDRESS",
			asset+"_BEP20_DEPOSIT_ADDRESS",
			"GRAPH_"+asset+"_BSC_DEPOSIT_ADDRESS",
			"BINANCE_"+asset+"_BSC_DEPOSIT_ADDRESS",
			"BYBIT_"+asset+"_BSC_DEPOSIT_ADDRESS",
		)
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
