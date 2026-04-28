package kyc

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"convert-chain/go-engine/internal/kyc/sumsub"

	"github.com/google/uuid"
)

type fakeSumsubProvider struct {
	createApplicant *sumsub.Applicant
	createErr       error
	existing        *sumsub.Applicant
	fetchErr        error
	link            *sumsub.WebSDKLink
	linkErr         error
	createCalls     int
	fetchCalls      int
	linkCalls       int
}

func (f *fakeSumsubProvider) Enabled() bool                                        { return true }
func (f *fakeSumsubProvider) IsSandbox() bool                                      { return false }
func (f *fakeSumsubProvider) VerifyWebhookSignature(_ []byte, _, _, _ string) bool { return true }

func (f *fakeSumsubProvider) CreateApplicant(_ context.Context, _ sumsub.ApplicantRequest) (*sumsub.Applicant, error) {
	f.createCalls++
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.createApplicant, nil
}

func (f *fakeSumsubProvider) GetApplicantByExternalUserID(_ context.Context, _ string) (*sumsub.Applicant, error) {
	f.fetchCalls++
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	return f.existing, nil
}

func (f *fakeSumsubProvider) CreateWebSDKLink(_ context.Context, _ sumsub.WebSDKLinkRequest) (*sumsub.WebSDKLink, error) {
	f.linkCalls++
	if f.linkErr != nil {
		return nil, f.linkErr
	}
	return f.link, nil
}

func TestSubmitSumsubKYCCreatesNewApplicant(t *testing.T) {
	provider := &fakeSumsubProvider{
		createApplicant: &sumsub.Applicant{ID: "applicant-new", ReviewStatus: "init"},
		link:            &sumsub.WebSDKLink{URL: "https://sumsub.example/link"},
	}
	orchestrator := &KYCOrchestrator{sumsub: provider}

	result, err := orchestrator.SubmitSumsubKYC(context.Background(), validSumsubKYCRequest())
	if err != nil {
		t.Fatalf("SubmitSumsubKYC returned error: %v", err)
	}
	if result.ProviderRef != "applicant-new" || result.Status != "PENDING" || result.VerificationURL == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if provider.fetchCalls != 0 || provider.linkCalls != 1 {
		t.Fatalf("unexpected call counts: fetch=%d link=%d", provider.fetchCalls, provider.linkCalls)
	}
}

func TestSubmitSumsubKYCDuplicateFetchesExistingApplicantAndReturnsLink(t *testing.T) {
	provider := &fakeSumsubProvider{
		createErr: duplicateApplicantError(),
		existing:  &sumsub.Applicant{ID: "applicant-existing", ReviewStatus: "init"},
		link:      &sumsub.WebSDKLink{URL: "https://sumsub.example/fresh-link"},
	}
	orchestrator := &KYCOrchestrator{sumsub: provider}

	result, err := orchestrator.SubmitSumsubKYC(context.Background(), validSumsubKYCRequest())
	if err != nil {
		t.Fatalf("SubmitSumsubKYC returned error: %v", err)
	}
	if result.ProviderRef != "applicant-existing" || result.Status != "PENDING" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.VerificationURL != "https://sumsub.example/fresh-link" {
		t.Fatalf("expected fresh link, got %s", result.VerificationURL)
	}
	if provider.fetchCalls != 1 || provider.linkCalls != 1 {
		t.Fatalf("unexpected call counts: fetch=%d link=%d", provider.fetchCalls, provider.linkCalls)
	}
}

func TestSubmitSumsubKYCDuplicateFetchFailureReturnsError(t *testing.T) {
	provider := &fakeSumsubProvider{
		createErr: duplicateApplicantError(),
		fetchErr:  &sumsub.ProviderError{Operation: "get_applicant_by_external_user_id", Code: "SUMSUB_HTTP_502", StatusCode: http.StatusBadGateway, Message: "downstream unavailable"},
	}
	orchestrator := &KYCOrchestrator{sumsub: provider}

	_, err := orchestrator.SubmitSumsubKYC(context.Background(), validSumsubKYCRequest())
	if err == nil {
		t.Fatal("expected error")
	}
	if provider.fetchCalls != 1 || provider.linkCalls != 0 {
		t.Fatalf("unexpected call counts: fetch=%d link=%d", provider.fetchCalls, provider.linkCalls)
	}
}

func TestSubmitSumsubKYCDuplicateApprovedSyncsStatusWithoutLink(t *testing.T) {
	applicant := &sumsub.Applicant{ID: "applicant-approved", ReviewStatus: "completed"}
	applicant.ReviewResult.ReviewAnswer = "GREEN"
	provider := &fakeSumsubProvider{
		createErr: duplicateApplicantError(),
		existing:  applicant,
	}
	orchestrator := &KYCOrchestrator{sumsub: provider}

	result, err := orchestrator.SubmitSumsubKYC(context.Background(), validSumsubKYCRequest())
	if err != nil {
		t.Fatalf("SubmitSumsubKYC returned error: %v", err)
	}
	if result.Status != "APPROVED" || result.ProviderRef != "applicant-approved" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if provider.linkCalls != 0 {
		t.Fatalf("expected no link generation for approved applicant, got %d", provider.linkCalls)
	}
}

func TestSubmitSumsubKYCDuplicateRejectedReturnsRejectedStatus(t *testing.T) {
	applicant := &sumsub.Applicant{ID: "applicant-rejected", ReviewStatus: "completed"}
	applicant.ReviewResult.ReviewAnswer = "RED"
	applicant.ReviewResult.ModerationComment = "Document mismatch"
	provider := &fakeSumsubProvider{
		createErr: duplicateApplicantError(),
		existing:  applicant,
	}
	orchestrator := &KYCOrchestrator{sumsub: provider}

	result, err := orchestrator.SubmitSumsubKYC(context.Background(), validSumsubKYCRequest())
	if err != nil {
		t.Fatalf("SubmitSumsubKYC returned error: %v", err)
	}
	if result.Status != "REJECTED" || result.Tier != "TIER_0" || result.Reason == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if provider.linkCalls != 0 {
		t.Fatalf("expected no link generation for rejected applicant, got %d", provider.linkCalls)
	}
}

func TestSafeProviderErrorMessageRedactsSensitiveNumbers(t *testing.T) {
	message := "Applicant with external user id 11111111-1111-1111-1111-111111111111 and phone +2348012345678 already exists"
	safe := safeProviderErrorMessage(message)
	if safe == message {
		t.Fatal("expected message to be redacted")
	}
	if containsAny(safe, []string{"11111111-1111", "+2348012345678"}) {
		t.Fatalf("sensitive value was not redacted: %s", safe)
	}
}

func validSumsubKYCRequest() SumsubKYCRequest {
	return SumsubKYCRequest{
		UserID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		TargetTier:  "TIER_1",
		LevelName:   "convertchain-tier1",
		FirstName:   "Test",
		LastName:    "User",
		DateOfBirth: "1995-01-01",
		PhoneNumber: "+2348012345678",
		TTLInSecs:   1800,
	}
}

func duplicateApplicantError() error {
	return &sumsub.ProviderError{
		Operation:  "create_applicant",
		Code:       "SUMSUB_HTTP_409",
		Message:    "Applicant with external user id already exists",
		StatusCode: http.StatusConflict,
	}
}

func containsAny(value string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
