package handlers

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type ProviderWebhookService interface {
	HandleSmileIDWebhook(ctx context.Context, payload []byte, signature string, timestamp string) error
	HandleSumsubWebhook(ctx context.Context, payload []byte, digest string, algorithm string) error
	HandleGraphWebhook(ctx context.Context, payload []byte, signature string) error
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

	if err := h.svc.HandleGraphWebhook(c.Request.Context(), body, signature); err != nil {
		status, response := providerWebhookErrorResponse(err)
		c.JSON(status, response)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func providerWebhookErrorResponse(err error) (int, gin.H) {
	switch strings.TrimSpace(err.Error()) {
	case "invalid_webhook_signature":
		return http.StatusUnauthorized, gin.H{"error": "invalid webhook signature"}
	case "malformed_webhook_payload":
		return http.StatusBadRequest, gin.H{"error": "malformed webhook payload"}
	case "provider_not_configured":
		return http.StatusServiceUnavailable, gin.H{"error": "provider not configured"}
	default:
		return http.StatusInternalServerError, gin.H{"error": "webhook processing failed", "details": err.Error()}
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
