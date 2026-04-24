package handlers

import (
	"context"
	"net/http"
	"time"

	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/domain"
	"github.com/gin-gonic/gin"
)

type UserService interface {
	CreateOrGetUser(ctx context.Context, req dto.CreateUserRequest) (*domain.User, bool, error)
	RecordConsent(ctx context.Context, userID, version string, consentedAt time.Time) error
}

type UserHandler struct {
	svc UserService
}

func NewUserHandler(svc UserService) *UserHandler {
	return &UserHandler{svc: svc}
}

// CreateOrGetUser handles POST /api/v1/users.
// Idempotent - safe to call multiple times with the same telegram_id.
func (h *UserHandler) CreateOrGetUser(c *gin.Context) {
	var req dto.CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Invalid request body", err.Error()))
		return
	}

	user, isNew, err := h.svc.CreateOrGetUser(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to create or retrieve user", nil))
		return
	}

	status := "EXISTING"
	if isNew {
		status = "CREATED"
	}

	c.JSON(http.StatusOK, dto.CreateUserResponse{
		UserID:                 user.ID.String(),
		Status:                 status,
		KYCStatus:              user.Status,
		TransactionPasswordSet: user.TxnPasswordHash != nil && *user.TxnPasswordHash != "",
	})
}

// RecordConsent handles POST /api/v1/consent.
// Nigerian NDPA 2023 requires explicit, documented consent before processing PII.
func (h *UserHandler) RecordConsent(c *gin.Context) {
	var req dto.ConsentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Invalid request body", err.Error()))
		return
	}

	consentedAt, err := time.Parse(time.RFC3339, req.ConsentedAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "consented_at must be ISO-8601 format (e.g. 2024-01-15T14:30:00Z)", nil))
		return
	}

	if err := h.svc.RecordConsent(c.Request.Context(), req.UserID, req.ConsentVersion, consentedAt); err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to record consent", nil))
		return
	}

	c.JSON(http.StatusOK, dto.ConsentResponse{
		UserID:         req.UserID,
		ConsentVersion: req.ConsentVersion,
		Recorded:       true,
	})
}
