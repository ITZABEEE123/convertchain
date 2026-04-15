package handlers

import (
	"context"
	"net/http"

	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/domain"
	"github.com/gin-gonic/gin"
)

type TradeService interface {
	CreateTrade(ctx context.Context, req dto.CreateTradeRequest) (*domain.Trade, error)
	GetTrade(ctx context.Context, tradeID string) (*domain.Trade, error)
	GetUserKYCStatus(ctx context.Context, userID string) (string, error)
}

type TradeHandler struct {
	svc TradeService
}

func NewTradeHandler(svc TradeService) *TradeHandler {
	return &TradeHandler{svc: svc}
}

// CreateTrade handles POST /api/v1/trades.
// Converts an approved quote into a live trade with a deposit address.
func (h *TradeHandler) CreateTrade(c *gin.Context) {
	var req dto.CreateTradeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Invalid request body", err.Error()))
		return
	}

	kycStatus, err := h.svc.GetUserKYCStatus(c.Request.Context(), req.UserID)
	if err != nil || kycStatus != "APPROVED" {
		c.JSON(http.StatusForbidden, dto.NewError(dto.ErrCodeKYCNotApproved, "KYC approval required to create trades", nil))
		return
	}

	trade, err := h.svc.CreateTrade(c.Request.Context(), req)
	if err != nil {
		switch err.Error() {
		case "quote_not_found":
			c.JSON(http.StatusNotFound, dto.NewError(dto.ErrCodeNotFound, "Quote not found", nil))
		case "quote_expired":
			c.JSON(http.StatusGone, dto.NewError(dto.ErrCodeQuoteExpired, "This quote has expired. Please request a new quote.", nil))
		case "quote_already_used":
			c.JSON(http.StatusConflict, dto.NewError(dto.ErrCodeQuoteUsed, "This quote has already been used to create a trade.", nil))
		default:
			c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to create trade", nil))
		}
		return
	}

	c.JSON(http.StatusCreated, tradeToResponse(trade))
}

// GetTrade handles GET /api/v1/trades/:trade_id.
func (h *TradeHandler) GetTrade(c *gin.Context) {
	trade, err := h.svc.GetTrade(c.Request.Context(), c.Param("trade_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to retrieve trade", nil))
		return
	}
	if trade == nil {
		c.JSON(http.StatusNotFound, dto.NewError(dto.ErrCodeNotFound, "Trade not found", nil))
		return
	}
	c.JSON(http.StatusOK, tradeToResponse(trade))
}

func tradeToResponse(t *domain.Trade) dto.TradeResponse {
	depositAddress := ""
	if t.DepositAddress != nil {
		depositAddress = *t.DepositAddress
	}

	txHash := ""
	if t.DepositTxHash != nil {
		txHash = *t.DepositTxHash
	}

	payoutRef := ""
	if t.GraphPayoutID != nil {
		payoutRef = *t.GraphPayoutID
	}

	amountNGN := t.ToAmountExpected
	if t.ToAmountActual != nil {
		amountNGN = *t.ToAmountActual
	}

	return dto.TradeResponse{
		TradeID:        t.ID.String(),
		UserID:         t.UserID.String(),
		Status:         t.Status,
		DepositAddress: depositAddress,
		DepositNetwork: "",
		AmountUSDC:     float64(t.FromAmount),
		AmountNGN:      float64(amountNGN),
		Rate:           0,
		Fee:            float64(t.FeeAmount),
		ExpiresAt:      t.ExpiresAt.Format("2006-01-02T15:04:05Z"),
		CreatedAt:      t.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:      t.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		TxHash:         txHash,
		PayoutRef:      payoutRef,
	}
}
