package workers

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type DepositConfirmationPolicy struct {
	Currency               string
	Network                string
	DetectionConfirmations int
	FinalityConfirmations  int
	AmountToleranceMinor   int64
}

type DepositPolicySet struct {
	policies map[string]DepositConfirmationPolicy
}

func DefaultDepositPolicySet() DepositPolicySet {
	set := DepositPolicySet{policies: map[string]DepositConfirmationPolicy{}}
	set.Put(DepositConfirmationPolicy{
		Currency:               "BTC",
		Network:                "btc",
		DetectionConfirmations: 1,
		FinalityConfirmations:  2,
		AmountToleranceMinor:   0,
	})
	set.Put(DepositConfirmationPolicy{
		Currency:               "USDC",
		Network:                "ethereum",
		DetectionConfirmations: 1,
		FinalityConfirmations:  12,
		AmountToleranceMinor:   0,
	})
	set.Put(DepositConfirmationPolicy{
		Currency:               "USDC",
		Network:                "polygon",
		DetectionConfirmations: 1,
		FinalityConfirmations:  64,
		AmountToleranceMinor:   0,
	})
	set.Put(DepositConfirmationPolicy{
		Currency:               "ETH",
		Network:                "ethereum",
		DetectionConfirmations: 1,
		FinalityConfirmations:  12,
		AmountToleranceMinor:   0,
	})
	set.Put(DepositConfirmationPolicy{
		Currency:               "BNB",
		Network:                "bsc",
		DetectionConfirmations: 1,
		FinalityConfirmations:  20,
		AmountToleranceMinor:   0,
	})
	set.Put(DepositConfirmationPolicy{
		Currency:               "USDT",
		Network:                "ethereum",
		DetectionConfirmations: 1,
		FinalityConfirmations:  12,
		AmountToleranceMinor:   0,
	})
	set.Put(DepositConfirmationPolicy{
		Currency:               "USDT",
		Network:                "polygon",
		DetectionConfirmations: 1,
		FinalityConfirmations:  64,
		AmountToleranceMinor:   0,
	})
	return set
}

func NewDepositPolicySetFromEnv() DepositPolicySet {
	set := DefaultDepositPolicySet()
	set.overrideFromEnv("BTC", "btc", "BTC_DEPOSIT_DETECTION_CONFIRMATIONS", "BTC_DEPOSIT_FINALITY_CONFIRMATIONS", "BTC_DEPOSIT_AMOUNT_TOLERANCE_MINOR")
	set.overrideFromEnv("USDC", "ethereum", "USDC_ETH_DEPOSIT_DETECTION_CONFIRMATIONS", "USDC_ETH_DEPOSIT_FINALITY_CONFIRMATIONS", "USDC_ETH_DEPOSIT_AMOUNT_TOLERANCE_MINOR")
	set.overrideFromEnv("USDC", "polygon", "USDC_POLYGON_DEPOSIT_DETECTION_CONFIRMATIONS", "USDC_POLYGON_DEPOSIT_FINALITY_CONFIRMATIONS", "USDC_POLYGON_DEPOSIT_AMOUNT_TOLERANCE_MINOR")
	set.overrideFromEnv("ETH", "ethereum", "ETH_DEPOSIT_DETECTION_CONFIRMATIONS", "ETH_DEPOSIT_FINALITY_CONFIRMATIONS", "ETH_DEPOSIT_AMOUNT_TOLERANCE_MINOR")
	set.overrideFromEnv("BNB", "bsc", "BNB_BSC_DEPOSIT_DETECTION_CONFIRMATIONS", "BNB_BSC_DEPOSIT_FINALITY_CONFIRMATIONS", "BNB_BSC_DEPOSIT_AMOUNT_TOLERANCE_MINOR")
	set.overrideFromEnv("USDT", "ethereum", "USDT_ETH_DEPOSIT_DETECTION_CONFIRMATIONS", "USDT_ETH_DEPOSIT_FINALITY_CONFIRMATIONS", "USDT_ETH_DEPOSIT_AMOUNT_TOLERANCE_MINOR")
	set.overrideFromEnv("USDT", "polygon", "USDT_POLYGON_DEPOSIT_DETECTION_CONFIRMATIONS", "USDT_POLYGON_DEPOSIT_FINALITY_CONFIRMATIONS", "USDT_POLYGON_DEPOSIT_AMOUNT_TOLERANCE_MINOR")
	return set
}

func (s *DepositPolicySet) Put(policy DepositConfirmationPolicy) {
	if s.policies == nil {
		s.policies = map[string]DepositConfirmationPolicy{}
	}
	currency := strings.ToUpper(strings.TrimSpace(policy.Currency))
	network := normalizeNetworkName(policy.Network)
	if currency == "" || network == "" {
		return
	}
	if policy.DetectionConfirmations <= 0 {
		policy.DetectionConfirmations = 1
	}
	if policy.FinalityConfirmations < policy.DetectionConfirmations {
		policy.FinalityConfirmations = policy.DetectionConfirmations
	}
	policy.Currency = currency
	policy.Network = network
	s.policies[policyKey(currency, network)] = policy
}

func (s DepositPolicySet) Resolve(currency, network string) (DepositConfirmationPolicy, bool) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	network = normalizeNetworkName(network)
	if policy, ok := s.policies[policyKey(currency, network)]; ok {
		return policy, true
	}
	if policy, ok := s.policies[policyKey(currency, "default")]; ok {
		return policy, true
	}
	return DepositConfirmationPolicy{}, false
}

func (s *DepositPolicySet) overrideFromEnv(currency, network, detectionKey, finalityKey, toleranceKey string) {
	policy, ok := s.Resolve(currency, network)
	if !ok {
		policy = DepositConfirmationPolicy{Currency: currency, Network: network, DetectionConfirmations: 1, FinalityConfirmations: 1}
	}
	policy.DetectionConfirmations = parseEnvInt(detectionKey, policy.DetectionConfirmations)
	policy.FinalityConfirmations = parseEnvInt(finalityKey, policy.FinalityConfirmations)
	policy.AmountToleranceMinor = parseEnvInt64(toleranceKey, policy.AmountToleranceMinor)
	s.Put(policy)
}

func parseExpectedDepositNetworkAndAddress(currency, depositAddress string) (string, string) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	trimmed := strings.TrimSpace(depositAddress)
	if strings.HasPrefix(strings.ToLower(trimmed), "sandbox://") {
		return "sandbox", trimmed
	}

	if strings.Contains(trimmed, ":") {
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) == 2 {
			network := normalizeNetworkName(parts[0])
			address := strings.TrimSpace(parts[1])
			if network != "" && address != "" {
				return network, address
			}
		}
	}

	switch currency {
	case "BTC":
		return "btc", trimmed
	case "ETH":
		return "ethereum", trimmed
	case "BNB":
		return "bsc", trimmed
	case "USDC", "USDT":
		network := normalizeNetworkName(os.Getenv(currency + "_DEPOSIT_NETWORK"))
		if network == "" || network == "default" {
			network = "ethereum"
		}
		return network, trimmed
	default:
		return "default", trimmed
	}
}

func normalizeNetworkName(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "", "default":
		return "default"
	case "btc", "bitcoin", "mainnet", "bitcoin-mainnet":
		return "btc"
	case "eth", "ethereum", "erc20", "eth-mainnet":
		return "ethereum"
	case "matic", "polygon", "polygon-pos":
		return "polygon"
	case "bsc", "bnb", "bnb-smart-chain", "binance-smart-chain", "bep20":
		return "bsc"
	case "sandbox":
		return "sandbox"
	default:
		return value
	}
}

func policyKey(currency, network string) string {
	return strings.ToUpper(strings.TrimSpace(currency)) + ":" + normalizeNetworkName(network)
}

func parseEnvInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func parseEnvInt64(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func amountOutsideTolerance(expected, observed, tolerance int64) bool {
	delta := observed - expected
	if delta < 0 {
		delta = -delta
	}
	return delta > tolerance
}

func formatDepositMismatchReason(currency, network, reason string, expected, observed int64) string {
	base := strings.TrimSpace(reason)
	if base == "" {
		base = "deposit_mismatch_detected"
	}
	if expected == observed {
		return fmt.Sprintf("%s currency=%s network=%s", base, strings.ToUpper(strings.TrimSpace(currency)), normalizeNetworkName(network))
	}
	return fmt.Sprintf("%s currency=%s network=%s expected=%d observed=%d", base, strings.ToUpper(strings.TrimSpace(currency)), normalizeNetworkName(network), expected, observed)
}
