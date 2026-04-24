package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/service"
	"github.com/gin-gonic/gin"
)

type AccountService interface {
	GetDeletionQuota(ctx context.Context, userID string) (int, error)
	DeleteAccount(ctx context.Context, userID, confirmationText, transactionPassword string) (deletedAtTime time.Time, err error)
}

type AccountHandler struct {
	svc AccountService
}

func NewAccountHandler(svc AccountService) *AccountHandler {
	return &AccountHandler{svc: svc}
}

func (h *AccountHandler) GetDeletionQuota(c *gin.Context) {
	userID := c.Param("user_id")
	remaining, err := h.svc.GetDeletionQuota(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to fetch deletion quota", nil))
		return
	}

	c.JSON(http.StatusOK, dto.DeleteAccountQuotaResponse{
		UserID:             userID,
		RemainingDeletions: remaining,
		WindowDays:         7,
	})
}

func (h *AccountHandler) DeleteAccount(c *gin.Context) {
	var req dto.DeleteAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Invalid request body", err.Error()))
		return
	}

	deletedAt, err := h.svc.DeleteAccount(c.Request.Context(), req.UserID, req.ConfirmationText, req.TransactionPassword)
	if err != nil {
		var activeTradeErr *service.ActiveTradeBlockError
		if errors.As(err, &activeTradeErr) {
			c.JSON(http.StatusConflict, dto.NewError(dto.ErrCodeDeletionBlocked, "Account deletion is blocked while you still have active trades or disputes.", gin.H{
				"trade_id":  activeTradeErr.TradeID,
				"trade_ref": activeTradeErr.TradeRef,
				"status":    activeTradeErr.Status,
			}))
			return
		}

		switch err.Error() {
		case "delete_confirmation_required":
			c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Type DELETE to confirm account deletion.", nil))
		case "deletion_quota_exceeded":
			c.JSON(http.StatusTooManyRequests, dto.NewError(dto.ErrCodeDeletionQuota, "You have reached the weekly account deletion limit.", nil))
		case "transaction_password_not_set":
			c.JSON(http.StatusConflict, dto.NewError(dto.ErrCodeTxnPasswordMissing, "Set a transaction password before deleting your account.", nil))
		case "transaction_password_invalid":
			c.JSON(http.StatusForbidden, dto.NewError(dto.ErrCodeTxnPasswordInvalid, "The transaction password is incorrect.", nil))
		case "transaction_password_locked":
			c.JSON(http.StatusLocked, dto.NewError(dto.ErrCodeTxnPasswordLocked, "Transaction password is temporarily locked. Please try again later.", nil))
		case "active_trade_exists":
			c.JSON(http.StatusConflict, dto.NewError(dto.ErrCodeDeletionBlocked, "Account deletion is blocked while you still have active trades or disputes.", nil))
		case "account_inactive":
			c.JSON(http.StatusConflict, dto.NewError(dto.ErrCodeValidation, "This account is already inactive.", nil))
		default:
			c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to delete account", nil))
		}
		return
	}

	c.JSON(http.StatusOK, dto.DeleteAccountResponse{
		UserID:    req.UserID,
		Deleted:   true,
		DeletedAt: deletedAt.Format("2006-01-02T15:04:05Z"),
	})
}
