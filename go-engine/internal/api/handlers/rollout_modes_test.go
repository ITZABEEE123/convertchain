package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/domain"

	"github.com/gin-gonic/gin"
)

type tradeServiceStub struct {
	createCalls int
}

func (s *tradeServiceStub) CreateTrade(_ context.Context, _ dto.CreateTradeRequest) (*domain.Trade, error) {
	s.createCalls++
	return nil, nil
}

func (s *tradeServiceStub) ConfirmTrade(_ context.Context, _ dto.ConfirmTradeRequest) (*domain.Trade, error) {
	return nil, nil
}

func (s *tradeServiceStub) GetTrade(_ context.Context, _ string) (*domain.Trade, error) {
	return nil, nil
}

func (s *tradeServiceStub) GetLatestActiveTradeForUser(_ context.Context, _ string) (*domain.Trade, error) {
	return nil, nil
}

func (s *tradeServiceStub) GetTradeReceipt(_ context.Context, _ string) (*domain.TradeReceipt, error) {
	return nil, nil
}

func (s *tradeServiceStub) GetTradeStatusContext(_ context.Context, _ string) (*domain.TradeStatusContext, error) {
	return nil, nil
}

func (s *tradeServiceStub) GetUserKYCStatus(_ context.Context, _ string) (string, error) {
	return "APPROVED", nil
}

type providerWebhookServiceStub struct {
	graphCalls  int
	lastEventID string
}

func (s *providerWebhookServiceStub) HandleSmileIDWebhook(_ context.Context, _ []byte, _ string, _ string) error {
	return nil
}

func (s *providerWebhookServiceStub) HandleSumsubWebhook(_ context.Context, _ []byte, _ string, _ string) error {
	return nil
}

func (s *providerWebhookServiceStub) HandleGraphWebhook(_ context.Context, _ []byte, _ string, eventID string) error {
	s.graphCalls++
	s.lastEventID = eventID
	return nil
}

func TestCreateTradeEnforceModeBlocksLegacyEndpoint(t *testing.T) {
	t.Setenv("TRADE_CREATE_ENDPOINT_MODE", "enforce")
	gin.SetMode(gin.TestMode)

	svc := &tradeServiceStub{}
	handler := NewTradeHandler(svc)

	body := map[string]string{
		"user_id":         "11111111-1111-1111-1111-111111111111",
		"quote_id":        "22222222-2222-2222-2222-222222222222",
		"bank_account_id": "33333333-3333-3333-3333-333333333333",
	}
	payload, _ := json.Marshal(body)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/trades", bytes.NewReader(payload))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.CreateTrade(ctx)

	if rec.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d", rec.Code)
	}
	if svc.createCalls != 0 {
		t.Fatalf("expected CreateTrade service to not be called in enforce mode")
	}
}

func TestGraphWebhookEnforceModeRequiresEventID(t *testing.T) {
	t.Setenv("GRAPH_WEBHOOK_EVENT_ID_MODE", "enforce")
	gin.SetMode(gin.TestMode)

	svc := &providerWebhookServiceStub{}
	handler := NewProviderWebhookHandler(svc)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/webhooks/graph", bytes.NewReader([]byte(`{"event":"payout.completed"}`)))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.Header.Set("X-Graph-Signature", "sig")

	handler.Graph(ctx)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if svc.graphCalls != 0 {
		t.Fatalf("expected Graph service not called when event id is missing in enforce mode")
	}
}

func TestGraphWebhookWarnModeAllowsMissingEventID(t *testing.T) {
	t.Setenv("GRAPH_WEBHOOK_EVENT_ID_MODE", "warn")
	gin.SetMode(gin.TestMode)

	svc := &providerWebhookServiceStub{}
	handler := NewProviderWebhookHandler(svc)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/webhooks/graph", bytes.NewReader([]byte(`{"event":"payout.completed"}`)))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.Header.Set("X-Graph-Signature", "sig")

	handler.Graph(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if svc.graphCalls != 1 {
		t.Fatalf("expected Graph service call in warn mode")
	}
	if rec.Header().Get("Warning") == "" {
		t.Fatalf("expected warning header in warn mode for missing event id")
	}
}
