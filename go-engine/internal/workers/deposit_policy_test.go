package workers

import "testing"

func TestDepositPolicySetDefaultsAndOverrides(t *testing.T) {
	t.Setenv("BTC_DEPOSIT_DETECTION_CONFIRMATIONS", "2")
	t.Setenv("BTC_DEPOSIT_FINALITY_CONFIRMATIONS", "6")
	t.Setenv("USDC_ETH_DEPOSIT_FINALITY_CONFIRMATIONS", "20")
	t.Setenv("USDC_POLYGON_DEPOSIT_FINALITY_CONFIRMATIONS", "80")
	t.Setenv("USDC_POLYGON_DEPOSIT_AMOUNT_TOLERANCE_MINOR", "5")
	t.Setenv("BNB_BSC_DEPOSIT_FINALITY_CONFIRMATIONS", "25")
	t.Setenv("USDT_POLYGON_DEPOSIT_AMOUNT_TOLERANCE_MINOR", "7")

	policies := NewDepositPolicySetFromEnv()

	btc, ok := policies.Resolve("btc", "bitcoin")
	if !ok {
		t.Fatalf("expected BTC policy")
	}
	if btc.DetectionConfirmations != 2 || btc.FinalityConfirmations != 6 {
		t.Fatalf("unexpected BTC policy: %#v", btc)
	}

	eth, ok := policies.Resolve("USDC", "erc20")
	if !ok {
		t.Fatalf("expected USDC ethereum policy")
	}
	if eth.FinalityConfirmations != 20 {
		t.Fatalf("expected USDC ethereum finality 20, got %d", eth.FinalityConfirmations)
	}

	polygon, ok := policies.Resolve("USDC", "matic")
	if !ok {
		t.Fatalf("expected USDC polygon policy")
	}
	if polygon.FinalityConfirmations != 80 || polygon.AmountToleranceMinor != 5 {
		t.Fatalf("unexpected USDC polygon policy: %#v", polygon)
	}

	bnb, ok := policies.Resolve("BNB", "bep20")
	if !ok {
		t.Fatalf("expected BNB BSC policy")
	}
	if bnb.FinalityConfirmations != 25 {
		t.Fatalf("expected BNB BSC finality 25, got %d", bnb.FinalityConfirmations)
	}

	usdt, ok := policies.Resolve("USDT", "polygon")
	if !ok {
		t.Fatalf("expected USDT Polygon policy")
	}
	if usdt.AmountToleranceMinor != 7 {
		t.Fatalf("expected USDT Polygon tolerance 7, got %d", usdt.AmountToleranceMinor)
	}
}

func TestAmountOutsideTolerance(t *testing.T) {
	if amountOutsideTolerance(100, 102, 2) {
		t.Fatalf("expected amount within tolerance")
	}
	if !amountOutsideTolerance(100, 103, 2) {
		t.Fatalf("expected amount outside tolerance")
	}
	if amountOutsideTolerance(100, 98, 2) {
		t.Fatalf("expected lower amount within tolerance")
	}
}
