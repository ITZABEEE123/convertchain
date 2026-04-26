package service

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"convert-chain/go-engine/internal/statemachine"
)

func normalizeChannelType(raw string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "WHATSAPP":
		return "WHATSAPP", nil
	case "TELEGRAM":
		return "TELEGRAM", nil
	default:
		return "", fmt.Errorf("unsupported channel type %q", raw)
	}
}

func normalizePhoneNumber(raw string) string {
	value := strings.TrimSpace(raw)
	value = strings.ReplaceAll(value, " ", "")
	value = strings.ReplaceAll(value, "-", "")
	return value
}

func userStatusToAPI(status string) string {
	switch status {
	case string(statemachine.StateKYCApproved):
		return "APPROVED"
	case string(statemachine.StateKYCPending):
		return "PENDING"
	case string(statemachine.StateKYCRejected):
		return "REJECTED"
	case string(statemachine.StateKYCInProgress):
		return "IN_PROGRESS"
	default:
		return "NOT_STARTED"
	}
}

func userStatusToClient(status string) string {
	switch userStatusToAPI(status) {
	case "APPROVED":
		return "approved"
	case "PENDING":
		return "pending"
	case "REJECTED":
		return "rejected"
	case "IN_PROGRESS":
		return "in_progress"
	default:
		return "not_started"
	}
}

func assetDecimals(asset string) int {
	switch strings.ToUpper(asset) {
	case "BTC":
		return 8
	case "ETH", "BNB":
		return 18
	case "USDT", "USDC":
		return 6
	default:
		return 8
	}
}

func decimalStringToMinorUnits(input string, decimals int) (int64, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return 0, errors.New("amount is required")
	}
	if strings.HasPrefix(value, "-") {
		return 0, errors.New("amount must be positive")
	}

	parts := strings.SplitN(value, ".", 2)
	wholePart := parts[0]
	fractionPart := ""
	if len(parts) == 2 {
		fractionPart = parts[1]
	}
	if wholePart == "" {
		wholePart = "0"
	}
	if len(fractionPart) > decimals {
		fractionPart = fractionPart[:decimals]
	}
	for len(fractionPart) < decimals {
		fractionPart += "0"
	}

	combined := strings.TrimLeft(wholePart+fractionPart, "0")
	if combined == "" {
		return 0, nil
	}
	return strconv.ParseInt(combined, 10, 64)
}

func minorUnitsToFloat(value int64, decimals int) (float64, error) {
	if decimals <= 0 {
		return float64(value), nil
	}

	divisor := float64(1)
	for i := 0; i < decimals; i++ {
		divisor *= 10
	}
	return float64(value) / divisor, nil
}

func minorUnitsToDecimalString(value int64, asset string) string {
	decimals := assetDecimals(asset)
	if decimals == 0 {
		return strconv.FormatInt(value, 10)
	}

	sign := ""
	if value < 0 {
		sign = "-"
		value *= -1
	}

	raw := strconv.FormatInt(value, 10)
	if len(raw) <= decimals {
		raw = strings.Repeat("0", decimals-len(raw)+1) + raw
	}

	split := len(raw) - decimals
	whole := raw[:split]
	fraction := strings.TrimRight(raw[split:], "0")
	if fraction == "" {
		return sign + whole
	}
	return sign + whole + "." + fraction
}

func bankNameFromCode(code string) string {
	if bank := lookupBankByCode(code); bank != nil && strings.TrimSpace(bank.BankName) != "" {
		return bank.BankName
	}
	return "Nigerian Bank"
}

func fallbackCryptoPrice(asset string) float64 {
	switch strings.ToUpper(asset) {
	case "BTC":
		return 68000
	case "ETH":
		return 3200
	case "BNB":
		return 600
	case "USDT", "USDC":
		return 1
	default:
		return 1
	}
}
