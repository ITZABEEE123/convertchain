package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type kycServiceStub struct {
	summary *domain.KYCStatusSummary
	err     error
	lastReq dto.KYCSubmitRequest
}

func (s *kycServiceStub) SubmitKYC(_ context.Context, req dto.KYCSubmitRequest) (*domain.KYCStatusSummary, error) {
	s.lastReq = req
	return s.summary, s.err
}

func (s *kycServiceStub) GetKYCStatus(_ context.Context, _ string) (*domain.KYCStatusSummary, error) {
	return s.summary, s.err
}

type fakeSafeKYCError struct {
	code       string
	message    string
	statusCode int
	details    map[string]interface{}
}

func (e fakeSafeKYCError) Error() string {
	return e.code
}

func (e fakeSafeKYCError) SafeCode() string {
	return e.code
}

func (e fakeSafeKYCError) SafeMessage() string {
	return e.message
}

func (e fakeSafeKYCError) StatusCode() int {
	return e.statusCode
}

func (e fakeSafeKYCError) DetailsMap() map[string]interface{} {
	return e.details
}

func TestKYCSubmitMissingRequiredFieldsReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewKYCHandler(&kycServiceStub{})
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/kyc/submit", bytes.NewReader([]byte(`{"user_id":""}`)))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.SubmitKYC(ctx)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	var body dto.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != dto.ErrCodeValidation {
		t.Fatalf("expected VALIDATION_ERROR, got %s", body.Error.Code)
	}
}

func TestKYCSubmitUserNotFoundReturns404(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewKYCHandler(&kycServiceStub{err: errors.New("user_not_found")})
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/kyc/submit", bytes.NewReader(validKYCPayload(t)))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.SubmitKYC(ctx)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	var body dto.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != dto.ErrCodeUserNotFound {
		t.Fatalf("expected USER_NOT_FOUND, got %s", body.Error.Code)
	}
}

func TestKYCSubmitSumsubSuccessReturnsPendingSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	handler := NewKYCHandler(&kycServiceStub{summary: &domain.KYCStatusSummary{
		UserID:          userID,
		Status:          "PENDING",
		Tier:            "TIER_1",
		Provider:        "sumsub",
		ProviderRef:     "applicant-123",
		ProviderStatus:  "init",
		LevelName:       "convertchain-tier1",
		VerificationURL: "https://example.test/sumsub",
	}})
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/kyc/submit", bytes.NewReader(validKYCPayload(t)))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.SubmitKYC(ctx)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}
	var body dto.KYCStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Provider != "sumsub" || body.VerificationURL == "" {
		t.Fatalf("expected sumsub pending response with verification URL, got %+v", body)
	}
}

func TestKYCSubmitProviderConfigurationErrorReturns502(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewKYCHandler(&kycServiceStub{err: fakeSafeKYCError{
		code:       dto.ErrCodeProviderConfiguration,
		message:    "KYC provider is not configured",
		statusCode: http.StatusBadGateway,
		details:    map[string]interface{}{"provider": "sumsub"},
	}})
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/kyc/submit", bytes.NewReader(validKYCPayload(t)))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.SubmitKYC(ctx)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
	var body dto.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != dto.ErrCodeProviderConfiguration {
		t.Fatalf("expected PROVIDER_CONFIGURATION_ERROR, got %s", body.Error.Code)
	}
}

func TestKYCSubmitDuplicateSubmissionReturns409(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewKYCHandler(&kycServiceStub{err: fakeSafeKYCError{
		code:       dto.ErrCodeDuplicateKYCSubmission,
		message:    "KYC submission already exists",
		statusCode: http.StatusConflict,
		details:    map[string]interface{}{"provider": "sumsub"},
	}})
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/kyc/submit", bytes.NewReader(validKYCPayload(t)))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.SubmitKYC(ctx)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
	var body dto.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != dto.ErrCodeDuplicateKYCSubmission {
		t.Fatalf("expected DUPLICATE_KYC_SUBMISSION, got %s", body.Error.Code)
	}
}

func validKYCPayload(t *testing.T) []byte {
	t.Helper()
	payload := map[string]string{
		"user_id":       "11111111-1111-1111-1111-111111111111",
		"first_name":    "Test",
		"last_name":     "User",
		"phone_number":  "+2348012345678",
		"date_of_birth": "1995-01-01",
		"nin":           "12345678901",
		"bvn":           "12345678901",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return body
}
