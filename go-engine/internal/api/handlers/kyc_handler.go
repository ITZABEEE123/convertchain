package handlers

import (
	"context"
	"net/http"
	"time"

	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/domain"
	"github.com/gin-gonic/gin"
)

type KYCService interface {
	SubmitKYC(ctx context.Context, req dto.KYCSubmitRequest) error
	GetKYCStatus(ctx context.Context, userID string) (*domain.KYCDocument, error)
}

type KYCHandler struct {
	svc KYCService
}

func NewKYCHandler(svc KYCService) *KYCHandler {
	return &KYCHandler{svc: svc}
}

// SubmitKYC handles POST /api/v1/kyc/submit.
// Returns 202 Accepted - verification is asynchronous (SmileID calls us back via webhook).
func (h *KYCHandler) SubmitKYC(c *gin.Context) {
	var req dto.KYCSubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Invalid request body", err.Error()))
		return
	}

	if err := h.svc.SubmitKYC(c.Request.Context(), req); err != nil {
		switch err.Error() {
		case "user_not_found":
			c.JSON(http.StatusNotFound, dto.NewError(dto.ErrCodeNotFound, "User not found", nil))
		case "kyc_already_approved":
			c.JSON(http.StatusConflict, dto.NewError("KYC_ALREADY_APPROVED", "This user has already been KYC approved", nil))
		default:
			c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to submit KYC", nil))
		}
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message": "KYC submission received. Verification is in progress.",
		"status":  "PENDING",
	})
}

// GetKYCStatus handles GET /api/v1/kyc/status/:user_id.
func (h *KYCHandler) GetKYCStatus(c *gin.Context) {
	userID := c.Param("user_id")

	record, err := h.svc.GetKYCStatus(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to retrieve KYC status", nil))
		return
	}

	if record == nil {
		c.JSON(http.StatusOK, dto.KYCStatusResponse{UserID: userID, Status: "NOT_STARTED"})
		return
	}

	resp := dto.KYCStatusResponse{
		UserID:      userID,
		Status:      deriveKYCStatus(record),
		SubmittedAt: record.CreatedAt.Format(time.RFC3339),
	}
	if record.Provider != nil {
		resp.Provider = *record.Provider
	}
	if record.VerifiedAt != nil {
		resp.CompletedAt = record.VerifiedAt.Format(time.RFC3339)
	}
	if record.RejectedReason != nil {
		resp.RejectionReason = *record.RejectedReason
	}

	c.JSON(http.StatusOK, resp)
}

func deriveKYCStatus(record *domain.KYCDocument) string {
	if record.Verified != nil {
		if *record.Verified {
			return "APPROVED"
		}
		return "REJECTED"
	}
	return "PENDING"
}
