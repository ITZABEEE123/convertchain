package handlers

import (
	"context"
	"net/http"
	"time"

	"convert-chain/go-engine/internal/api/dto"
	"github.com/gin-gonic/gin"
)

type SecurityService interface {
	SetupTransactionPassword(ctx context.Context, userID, password string) error
}

type SecurityHandler struct {
	svc SecurityService
}

func NewSecurityHandler(svc SecurityService) *SecurityHandler {
	return &SecurityHandler{svc: svc}
}

func (h *SecurityHandler) SetupTransactionPassword(c *gin.Context) {
	var req dto.SetupTransactionPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Invalid request body", err.Error()))
		return
	}

	if req.TransactionPassword != req.ConfirmPassword {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Transaction password confirmation does not match.", nil))
		return
	}

	if err := h.svc.SetupTransactionPassword(c.Request.Context(), req.UserID, req.TransactionPassword); err != nil {
		switch err.Error() {
		case "account_inactive":
			c.JSON(http.StatusConflict, dto.NewError(dto.ErrCodeValidation, "This account is inactive and cannot set a transaction password.", nil))
		default:
			c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, err.Error(), nil))
		}
		return
	}

	c.JSON(http.StatusOK, dto.SetupTransactionPasswordResponse{
		UserID:                 req.UserID,
		TransactionPasswordSet: true,
		SetAt:                  time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	})
}
