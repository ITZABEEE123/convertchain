package service

import (
	"context"
	"strings"
	"testing"

	"convert-chain/go-engine/internal/domain"

	"github.com/google/uuid"
)

func TestTierLimitPolicyForTierSupportsEnvOverride(t *testing.T) {
	t.Setenv("KYC_TIER_1_DAILY_LIMIT_KOBO", "123456")
	policy := tierLimitPolicyForTier("tier_1")
	if policy.DailyKobo != 123456 {
		t.Fatalf("expected daily override, got %d", policy.DailyKobo)
	}
	if policy.MonthlyKobo <= 0 {
		t.Fatalf("expected default monthly limit > 0")
	}
}

func TestEvaluateScreeningChecksBlocksSanctionsMatch(t *testing.T) {
	t.Setenv("SANCTIONS_BLOCK_TERMS", "DOE")
	t.Setenv("PEP_ESCALATE_TERMS", "")

	svc := &ApplicationService{}
	user := &domain.User{ID: uuid.New(), FirstName: "John", LastName: "Doe"}

	err := svc.evaluateScreeningChecks(context.Background(), user, nil, nil, nil, "")
	if err == nil || err.Error() != "screening_blocked" {
		t.Fatalf("expected screening_blocked, got %v", err)
	}
}

func TestEvaluateScreeningChecksEscalatesPEPMatch(t *testing.T) {
	t.Setenv("SANCTIONS_BLOCK_TERMS", "")
	t.Setenv("PEP_ESCALATE_TERMS", "SENATOR")

	svc := &ApplicationService{}
	user := &domain.User{ID: uuid.New(), FirstName: "Senator", LastName: "Ada"}

	err := svc.evaluateScreeningChecks(context.Background(), user, nil, nil, nil, "")
	if err == nil || err.Error() != "screening_review_required" {
		t.Fatalf("expected screening_review_required, got %v", err)
	}
}

func TestEvaluateQuoteMonitoringCreatesFailedKYCAlert(t *testing.T) {
	svc := &ApplicationService{}
	user := &domain.User{ID: uuid.New(), Status: "KYC_REJECTED"}

	err := svc.evaluateQuoteMonitoring(context.Background(), user, 10_000, nil)
	if err == nil || err.Error() != "compliance_review_required" {
		t.Fatalf("expected compliance_review_required, got %v", err)
	}
	if !strings.Contains(err.(*ComplianceReviewRequiredError).Reason, "case_ref=") {
		t.Fatalf("expected synthetic aml case reference in reason, got %q", err.(*ComplianceReviewRequiredError).Reason)
	}
}

func TestIsWalletBlockedMatchesConfiguredAddress(t *testing.T) {
	t.Setenv("HIGH_RISK_WALLET_BLOCKLIST", "bc1-risk-addr, 0xabc")
	if !isWalletBlocked("0xabc") {
		t.Fatalf("expected wallet blocklist match")
	}
	if isWalletBlocked("0xdef") {
		t.Fatalf("did not expect unmatched wallet to be blocked")
	}
}
