package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"convert-chain/go-engine/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type adminComplianceServiceStub struct{}

func (s *adminComplianceServiceStub) ListTradeDisputes(_ context.Context, _ string, _ int) ([]*domain.TradeDispute, error) {
	return nil, nil
}

func (s *adminComplianceServiceStub) GetTradeDispute(_ context.Context, _ string) (*domain.TradeDispute, error) {
	return nil, nil
}

func (s *adminComplianceServiceStub) ResolveTradeDispute(_ context.Context, _ string, _ string, _ string, _ string) (*domain.TradeDispute, *domain.Trade, error) {
	return nil, nil, nil
}

func (s *adminComplianceServiceStub) GetProviderReadiness(_ context.Context) (*domain.ProviderReadinessReport, error) {
	return nil, nil
}

func (s *adminComplianceServiceStub) ListAMLCases(_ context.Context, _ string, _ int) ([]*domain.AMLReviewCase, error) {
	return []*domain.AMLReviewCase{}, nil
}

func (s *adminComplianceServiceStub) GetAMLCase(_ context.Context, _ string) (*domain.AMLReviewCase, error) {
	return nil, nil
}

func (s *adminComplianceServiceStub) DispositionAMLCase(_ context.Context, _ string, status string, disposition string, note string, strReferralMetadata map[string]interface{}, actor string) (*domain.AMLReviewCase, error) {
	noteCopy := note
	dispCopy := disposition
	return &domain.AMLReviewCase{
		ID:              uuid.New(),
		CaseRef:         "AML-CASE-1",
		CaseType:        "VELOCITY_ALERT",
		Severity:        "HIGH",
		Status:          status,
		Reason:          "review",
		Disposition:     &dispCopy,
		DispositionNote: &noteCopy,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}, nil
}

func (s *adminComplianceServiceStub) RecordLegalLaunchApproval(_ context.Context, environment string, approvalStatus string, approvedBy string, legalMemoRef string, notes string, evidence map[string]interface{}, signedAt *time.Time) (*domain.LegalLaunchApproval, error) {
	return &domain.LegalLaunchApproval{ID: uuid.New(), Environment: environment, ApprovalStatus: approvalStatus, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, nil
}

func (s *adminComplianceServiceStub) RecordDataProtectionEvent(_ context.Context, userID string, eventType string, status string, reference string, details map[string]interface{}, createdBy string) (*domain.DataProtectionEvent, error) {
	return &domain.DataProtectionEvent{ID: uuid.New(), EventType: eventType, Status: status, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, nil
}

func TestDispositionAMLCase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewAdminHandler(&adminComplianceServiceStub{})

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Params = gin.Params{{Key: "id", Value: "AML-CASE-1"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/compliance/cases/AML-CASE-1/disposition", bytes.NewBufferString(`{"status":"CONFIRMED","disposition":"confirmed_suspicious","disposition_note":"Escalated to STR","actor":"compliance_officer"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.DispositionAMLCase(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}
