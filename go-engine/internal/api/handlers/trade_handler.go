package handlers

import (
	"context"
	"net/http"
	"strconv"

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

	return dto.TradeResponse{
		TradeID:               t.ID.String(),
		UserID:                t.UserID.String(),
		Status:                t.Status,
		DepositAddress:        depositAddress,
		DepositAmount:         formatTradeAmount(t.FromAmount, t.FromCurrency),
		Asset:                 t.FromCurrency,
		ExpiresAt:             t.ExpiresAt.Format("2006-01-02T15:04:05Z"),
		CreatedAt:             t.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:             t.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		TxHash:                txHash,
		PayoutRef:             payoutRef,
		Confirmations:         t.Confirmations,
		RequiredConfirmations: 2,
	}
}

func formatTradeAmount(amount int64, currency string) string {
	decimals := 8
	switch currency {
	case "ETH":
		decimals = 18
	case "USDC", "USDT":
		decimals = 6
	case "BNB":
		decimals = 8
	}

	divisor := 1.0
	for i := 0; i < decimals; i++ {
		divisor *= 10
	}
	return strconv.FormatFloat(float64(amount)/divisor, 'f', -1, 64)
}
