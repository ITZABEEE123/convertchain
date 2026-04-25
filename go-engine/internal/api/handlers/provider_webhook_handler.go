package handlers

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"

	"convert-chain/go-engine/internal/api/dto"

	"github.com/gin-gonic/gin"
)

type ProviderWebhookService interface {
	HandleSmileIDWebhook(ctx context.Context, payload []byte, signature string, timestamp string) error
	HandleSumsubWebhook(ctx context.Context, payload []byte, digest string, algorithm string) error
	HandleGraphWebhook(ctx context.Context, payload []byte, signature string, eventID string) error
}

type ProviderWebhookHandler struct {
	svc ProviderWebhookService
}

func NewProviderWebhookHandler(svc ProviderWebhookService) *ProviderWebhookHandler {
	return &ProviderWebhookHandler{svc: svc}
}

func (h *ProviderWebhookHandler) SmileID(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	signature := firstNonEmptyHeader(
		c.GetHeader("SmileID-Request-Signature"),
		c.GetHeader("SmileID-Signature"),
	)
	timestamp := firstNonEmptyHeader(
		c.GetHeader("SmileID-Timestamp"),
		c.GetHeader("X-SmileID-Timestamp"),
	)

	if err := h.svc.HandleSmileIDWebhook(c.Request.Context(), body, signature, timestamp); err != nil {
		status, response := providerWebhookErrorResponse(err)
		c.JSON(status, response)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *ProviderWebhookHandler) Sumsub(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	digest := firstNonEmptyHeader(
		c.GetHeader("X-Payload-Digest"),
		c.GetHeader("x-payload-digest"),
	)
	algorithm := firstNonEmptyHeader(
		c.GetHeader("X-Payload-Digest-Alg"),
		c.GetHeader("x-payload-digest-alg"),
	)

	if err := h.svc.HandleSumsubWebhook(c.Request.Context(), body, digest, algorithm); err != nil {
		status, response := providerWebhookErrorResponse(err)
		c.JSON(status, response)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *ProviderWebhookHandler) Graph(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	signature := firstNonEmptyHeader(
		c.GetHeader("X-Graph-Signature"),
		c.GetHeader("Graph-Signature"),
		c.GetHeader("X-Webhook-Signature"),
		c.GetHeader("X-Signature"),
	)
	eventID := firstNonEmptyHeader(
		c.GetHeader("X-Graph-Event-Id"),
		c.GetHeader("Graph-Event-Id"),
		c.GetHeader("X-Webhook-Event-Id"),
	)

	eventIDMode := graphWebhookEventIDMode()
	if eventID == "" {
		if eventIDMode == "enforce" {
			c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeWebhookMissingEventID, "X-Graph-Event-Id header is required", nil))
			return
		}
		if eventIDMode == "warn" {
			c.Header("Warning", "299 - Missing Graph webhook event id header; replay protection is in compatibility mode")
		}
	}

	if err := h.svc.HandleGraphWebhook(c.Request.Context(), body, signature, eventID); err != nil {
		status, response := providerWebhookErrorResponse(err)
		c.JSON(status, response)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func providerWebhookErrorResponse(err error) (int, gin.H) {
	switch strings.TrimSpace(err.Error()) {
	case "invalid_webhook_signature":
		return http.StatusUnauthorized, gin.H{"error": dto.NewError(dto.ErrCodeWebhookInvalidSignature, "Invalid webhook signature", nil).Error}
	case "malformed_webhook_payload":
		return http.StatusBadRequest, gin.H{"error": dto.NewError(dto.ErrCodeWebhookMalformedPayload, "Malformed webhook payload", nil).Error}
	case "webhook_missing_event_id":
		return http.StatusBadRequest, gin.H{"error": dto.NewError(dto.ErrCodeWebhookMissingEventID, "Missing webhook event id", nil).Error}
	case "provider_not_configured":
		return http.StatusServiceUnavailable, gin.H{"error": dto.NewError(dto.ErrCodeWebhookProviderDisabled, "Provider is not configured", nil).Error}
	default:
		return http.StatusInternalServerError, gin.H{"error": dto.NewError(dto.ErrCodeWebhookProcessingFailed, "Webhook processing failed", err.Error()).Error}
	}
}

func graphWebhookEventIDMode() string {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("GRAPH_WEBHOOK_EVENT_ID_MODE")))
	switch value {
	case "off", "warn", "enforce":
		return value
	default:
		return "warn"
	}
}

func firstNonEmptyHeader(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
