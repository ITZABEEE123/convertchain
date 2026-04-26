package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/domain"
	"github.com/gin-gonic/gin"
)

type KYCService interface {
	SubmitKYC(ctx context.Context, req dto.KYCSubmitRequest) (*domain.KYCStatusSummary, error)
	GetKYCStatus(ctx context.Context, userID string) (*domain.KYCStatusSummary, error)
}

type KYCHandler struct {
	svc KYCService
}

type safeKYCSubmitError interface {
	error
	SafeCode() string
	SafeMessage() string
	StatusCode() int
	DetailsMap() map[string]interface{}
}

func NewKYCHandler(svc KYCService) *KYCHandler {
	return &KYCHandler{svc: svc}
}

func (h *KYCHandler) SubmitKYC(c *gin.Context) {
	var req dto.KYCSubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Invalid request body", err.Error()))
		return
	}

	summary, err := h.svc.SubmitKYC(c.Request.Context(), req)
	if err != nil {
		var safeErr safeKYCSubmitError
		if errors.As(err, &safeErr) {
			c.JSON(safeErr.StatusCode(), dto.NewError(safeErr.SafeCode(), safeErr.SafeMessage(), safeErr.DetailsMap()))
			return
		}

		switch err.Error() {
		case "user_not_found":
			c.JSON(http.StatusNotFound, dto.NewError(dto.ErrCodeUserNotFound, "User not found", nil))
		case "kyc_already_approved":
			c.JSON(http.StatusConflict, dto.NewError("KYC_ALREADY_APPROVED", "This user has already been KYC approved", nil))
		case "consent_required":
			c.JSON(http.StatusConflict, dto.NewError("CONSENT_REQUIRED", "User consent is required before KYC can start", nil))
		case "tier_upgrade_requires_approved_kyc":
			c.JSON(http.StatusConflict, dto.NewError("KYC_TIER_PREREQUISITE", "Tier 1 KYC must be approved before upgrading", nil))
		case "kyc_provider_not_configured":
			c.JSON(http.StatusBadGateway, dto.NewError(dto.ErrCodeProviderConfiguration, "KYC provider is not configured", nil))
		default:
			c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to submit KYC", nil))
		}
		return
	}

	resp := h.toStatusResponse(req.UserID, summary)
	if summary == nil {
		c.JSON(http.StatusAccepted, gin.H{
			"message": "KYC submission received. Verification is in progress.",
			"status":  "PENDING",
		})
		return
	}

	statusCode := http.StatusAccepted
	switch resp.Status {
	case "APPROVED", "REJECTED":
		statusCode = http.StatusOK
	}
	c.JSON(statusCode, resp)
}

// GetKYCStatus handles GET /api/v1/kyc/status/:user_id.
func (h *KYCHandler) GetKYCStatus(c *gin.Context) {
	userID := c.Param("user_id")

	summary, err := h.svc.GetKYCStatus(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to retrieve KYC status", nil))
		return
	}

	if summary == nil {
		c.JSON(http.StatusOK, dto.KYCStatusResponse{
			UserID:    userID,
			Status:    "NOT_STARTED",
			KYCStatus: "NOT_STARTED",
		})
		return
	}

	c.JSON(http.StatusOK, h.toStatusResponse(userID, summary))
}

func (h *KYCHandler) toStatusResponse(userID string, summary *domain.KYCStatusSummary) dto.KYCStatusResponse {
	resp := dto.KYCStatusResponse{
		UserID:    userID,
		Status:    "NOT_STARTED",
		KYCStatus: "NOT_STARTED",
	}
	if summary == nil {
		return resp
	}

	resp.UserID = summary.UserID.String()
	resp.Status = summary.Status
	resp.KYCStatus = summary.Status
	resp.Provider = summary.Provider
	resp.ProviderRef = summary.ProviderRef
	resp.ProviderStatus = summary.ProviderStatus
	resp.LevelName = summary.LevelName
	resp.VerificationURL = summary.VerificationURL
	resp.Tier = summary.Tier
	resp.TransactionPasswordSet = summary.TransactionPasswordSet
	if summary.SubmittedAt != nil {
		resp.SubmittedAt = summary.SubmittedAt.UTC().Format(time.RFC3339)
	}
	if summary.CompletedAt != nil {
		resp.CompletedAt = summary.CompletedAt.UTC().Format(time.RFC3339)
	}
	resp.RejectionReason = summary.RejectionReason
	return resp
}
