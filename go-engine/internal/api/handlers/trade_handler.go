package handlers

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/domain"

	"github.com/gin-gonic/gin"
)

type TradeService interface {
	CreateTrade(ctx context.Context, req dto.CreateTradeRequest) (*domain.Trade, error)
	ConfirmTrade(ctx context.Context, req dto.ConfirmTradeRequest) (*domain.Trade, error)
	GetTrade(ctx context.Context, tradeID string) (*domain.Trade, error)
	GetLatestActiveTradeForUser(ctx context.Context, userID string) (*domain.Trade, error)
	GetTradeReceipt(ctx context.Context, tradeID string) (*domain.TradeReceipt, error)
	GetTradeStatusContext(ctx context.Context, userID string) (*domain.TradeStatusContext, error)
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
	mode := legacyCreateTradeEndpointMode()
	if mode != "allow" {
		c.Header("Deprecation", "true")
		c.Header("Sunset", "2026-12-31")
		c.Header("Warning", "299 - Legacy trade creation endpoint is deprecated; migrate to /api/v1/trades/confirm")
	}
	if mode == "enforce" {
		c.JSON(http.StatusGone, dto.NewError(
			dto.ErrCodeEndpointDeprecated,
			"POST /api/v1/trades is deprecated for payout-bound trades. Use POST /api/v1/trades/confirm with transaction_password.",
			nil,
		))
		return
	}

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
		var detailErr interface {
			error
			DetailsMap() map[string]interface{}
		}
		switch err.Error() {
		case "quote_not_found":
			c.JSON(http.StatusNotFound, dto.NewError(dto.ErrCodeNotFound, "Quote not found", nil))
		case "quote_expired":
			c.JSON(http.StatusGone, dto.NewError(dto.ErrCodeQuoteExpired, "This quote has expired. Please request a new quote.", nil))
		case "quote_already_used":
			c.JSON(http.StatusConflict, dto.NewError(dto.ErrCodeQuoteUsed, "This quote has already been used to create a trade.", nil))
		case "limit_exceeded":
			if errors.As(err, &detailErr) {
				details := detailErr.DetailsMap()
				message := "KYC tier limit exceeded."
				if guidance, ok := details["guidance"].(string); ok && strings.TrimSpace(guidance) != "" {
					message = message + " " + guidance
				}
				c.JSON(http.StatusForbidden, dto.NewError(dto.ErrCodeLimitExceeded, message, details))
				return
			}
			c.JSON(http.StatusForbidden, dto.NewError(dto.ErrCodeLimitExceeded, "KYC tier limit exceeded.", nil))
		case "screening_blocked":
			c.JSON(http.StatusForbidden, dto.NewError(dto.ErrCodeScreeningBlocked, "Sanctions screening blocked this transaction. Contact compliance.", nil))
		case "screening_review_required":
			c.JSON(http.StatusConflict, dto.NewError(dto.ErrCodeScreeningReviewRequired, "Possible sanctions/PEP match requires compliance review before trading.", nil))
		case "compliance_review_required":
			c.JSON(http.StatusConflict, dto.NewError(dto.ErrCodeComplianceReviewRequired, "Transaction monitoring flagged this trade for compliance review.", nil))
		default:
			c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to create trade", nil))
		}
		return
	}

	c.JSON(http.StatusCreated, h.tradeToResponse(c, trade))
}

func legacyCreateTradeEndpointMode() string {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("TRADE_CREATE_ENDPOINT_MODE")))
	switch value {
	case "allow", "warn", "enforce":
		return value
	default:
		return "warn"
	}
}

// ConfirmTrade handles POST /api/v1/trades/confirm.
// This is the production bot path: verify password, consume quote, and create an authorized trade atomically.
func (h *TradeHandler) ConfirmTrade(c *gin.Context) {
	var req dto.ConfirmTradeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Invalid request body", err.Error()))
		return
	}

	kycStatus, err := h.svc.GetUserKYCStatus(c.Request.Context(), req.UserID)
	if err != nil || kycStatus != "APPROVED" {
		c.JSON(http.StatusForbidden, dto.NewError(dto.ErrCodeKYCNotApproved, "KYC approval required to create trades", nil))
		return
	}

	trade, err := h.svc.ConfirmTrade(c.Request.Context(), req)
	if err != nil {
		var tradePreflightErr interface {
			error
			DetailsMap() map[string]interface{}
		}
		var detailErr interface {
			error
			DetailsMap() map[string]interface{}
		}
		switch err.Error() {
		case "quote_not_found":
			c.JSON(http.StatusNotFound, dto.NewError(dto.ErrCodeNotFound, "Quote not found", nil))
		case "quote_expired":
			c.JSON(http.StatusGone, dto.NewError(dto.ErrCodeQuoteExpired, "This quote has expired. Please request a new quote.", nil))
		case "quote_already_used":
			c.JSON(http.StatusConflict, dto.NewError(dto.ErrCodeQuoteUsed, "This quote has already been used to create a trade.", nil))
		case "transaction_password_not_set":
			c.JSON(http.StatusConflict, dto.NewError(dto.ErrCodeTxnPasswordMissing, "Set a transaction password before confirming a trade.", nil))
		case "transaction_password_invalid":
			c.JSON(http.StatusForbidden, dto.NewError(dto.ErrCodeTxnPasswordInvalid, "The transaction password is incorrect.", nil))
		case "transaction_password_locked":
			c.JSON(http.StatusLocked, dto.NewError(dto.ErrCodeTxnPasswordLocked, "Transaction password is temporarily locked. Please try again later.", nil))
		case "limit_exceeded":
			if errors.As(err, &detailErr) {
				details := detailErr.DetailsMap()
				message := "KYC tier limit exceeded."
				if guidance, ok := details["guidance"].(string); ok && strings.TrimSpace(guidance) != "" {
					message = message + " " + guidance
				}
				c.JSON(http.StatusForbidden, dto.NewError(dto.ErrCodeLimitExceeded, message, details))
				return
			}
			c.JSON(http.StatusForbidden, dto.NewError(dto.ErrCodeLimitExceeded, "KYC tier limit exceeded.", nil))
		case "screening_blocked":
			c.JSON(http.StatusForbidden, dto.NewError(dto.ErrCodeScreeningBlocked, "Sanctions screening blocked this transaction. Contact compliance.", nil))
		case "screening_review_required":
			c.JSON(http.StatusConflict, dto.NewError(dto.ErrCodeScreeningReviewRequired, "Possible sanctions/PEP match requires compliance review before trading.", nil))
		case "compliance_review_required":
			c.JSON(http.StatusConflict, dto.NewError(dto.ErrCodeComplianceReviewRequired, "Transaction monitoring flagged this trade for compliance review.", nil))
		default:
			if errors.As(err, &tradePreflightErr) {
				c.JSON(http.StatusConflict, dto.NewError(dto.ErrCodeTradePreflightFailed, tradePreflightErr.Error(), tradePreflightErr.DetailsMap()))
				return
			}
			c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to confirm trade", nil))
		}
		return
	}

	c.JSON(http.StatusCreated, h.tradeToResponse(c, trade))
}

func (h *TradeHandler) GetTradeStatusContext(c *gin.Context) {
	contextResult, err := h.svc.GetTradeStatusContext(c.Request.Context(), c.Param("user_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to retrieve trade status context", nil))
		return
	}
	if contextResult == nil || contextResult.Trade == nil {
		c.JSON(http.StatusNotFound, dto.NewError(dto.ErrCodeNotFound, "Trade status context not found", nil))
		return
	}

	response := dto.TradeStatusContextResponse{
		ContextType:    contextResult.ContextType,
		HasActiveTrade: contextResult.ContextType == "active",
		Trade:          tradeResponsePointer(h.tradeToResponse(c, contextResult.Trade)),
	}
	if contextResult.Receipt != nil {
		receiptResponse := dto.TradeReceiptResponse{
			TradeID:             contextResult.Receipt.TradeID,
			TradeRef:            contextResult.Receipt.TradeRef,
			Status:              contextResult.Receipt.Status,
			PricingMode:         contextResult.Receipt.PricingMode,
			PayoutAmountKobo:    contextResult.Receipt.PayoutAmountKobo,
			FeeAmountKobo:       contextResult.Receipt.FeeAmountKobo,
			BankName:            contextResult.Receipt.BankName,
			MaskedAccountNumber: contextResult.Receipt.MaskedAccountNumber,
			PayoutRef:           contextResult.Receipt.PayoutRef,
			CreatedAt:           contextResult.Receipt.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
		if contextResult.Receipt.PayoutCompletedAt != nil {
			receiptResponse.PayoutCompletedAt = contextResult.Receipt.PayoutCompletedAt.Format("2006-01-02T15:04:05Z")
		}
		response.Receipt = &receiptResponse
	}
	if contextResult.Dispute != nil {
		disputeResponse := dto.TradeDisputeStatusResponse{
			DisputeID:      contextResult.Dispute.ID.String(),
			TicketRef:      contextResult.Dispute.TicketRef,
			Status:         contextResult.Dispute.Status,
			Source:         contextResult.Dispute.Source,
			Reason:         contextResult.Dispute.Reason,
			ResolutionMode: stringOrEmpty(contextResult.Dispute.ResolutionMode),
			ResolutionNote: stringOrEmpty(contextResult.Dispute.ResolutionNote),
		}
		if contextResult.Dispute.ResolvedAt != nil {
			disputeResponse.ResolvedAt = contextResult.Dispute.ResolvedAt.Format("2006-01-02T15:04:05Z")
		}
		response.Dispute = &disputeResponse
	}

	c.JSON(http.StatusOK, response)
}
func tradeResponsePointer(response dto.TradeResponse) *dto.TradeResponse {
	copyValue := response
	return &copyValue
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
	c.JSON(http.StatusOK, h.tradeToResponse(c, trade))
}

// GetActiveTrade handles GET /api/v1/users/:user_id/trades/active.
func (h *TradeHandler) GetActiveTrade(c *gin.Context) {
	trade, err := h.svc.GetLatestActiveTradeForUser(c.Request.Context(), c.Param("user_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to retrieve active trade", nil))
		return
	}
	if trade == nil {
		c.JSON(http.StatusNotFound, dto.NewError(dto.ErrCodeNotFound, "Active trade not found", nil))
		return
	}
	c.JSON(http.StatusOK, h.tradeToResponse(c, trade))
}

// GetTradeReceipt handles GET /api/v1/trades/:trade_id/receipt.
func (h *TradeHandler) GetTradeReceipt(c *gin.Context) {
	receipt, err := h.svc.GetTradeReceipt(c.Request.Context(), c.Param("trade_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to retrieve trade receipt", nil))
		return
	}
	if receipt == nil {
		c.JSON(http.StatusNotFound, dto.NewError(dto.ErrCodeNotFound, "Trade receipt not found", nil))
		return
	}

	response := dto.TradeReceiptResponse{
		TradeID:             receipt.TradeID,
		TradeRef:            receipt.TradeRef,
		Status:              receipt.Status,
		PricingMode:         receipt.PricingMode,
		PayoutAmountKobo:    receipt.PayoutAmountKobo,
		FeeAmountKobo:       receipt.FeeAmountKobo,
		BankName:            receipt.BankName,
		MaskedAccountNumber: receipt.MaskedAccountNumber,
		PayoutRef:           receipt.PayoutRef,
		CreatedAt:           receipt.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if receipt.PayoutCompletedAt != nil {
		response.PayoutCompletedAt = receipt.PayoutCompletedAt.Format("2006-01-02T15:04:05Z")
	}

	c.JSON(http.StatusOK, response)
}

func (h *TradeHandler) tradeToResponse(c *gin.Context, t *domain.Trade) dto.TradeResponse {
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

	var bankName string
	var maskedAccount string
	if receipt, err := h.svc.GetTradeReceipt(c.Request.Context(), t.ID.String()); err == nil && receipt != nil {
		bankName = receipt.BankName
		maskedAccount = receipt.MaskedAccountNumber
	}

	response := dto.TradeResponse{
		TradeID:               t.ID.String(),
		TradeRef:              t.TradeRef,
		UserID:                t.UserID.String(),
		Status:                t.Status,
		DisputeReason:         stringOrEmpty(t.DisputeReason),
		DepositAddress:        depositAddress,
		DepositAmount:         formatTradeAmount(t.FromAmount, t.FromCurrency),
		Asset:                 t.FromCurrency,
		NetAmountKobo:         t.ToAmountExpected,
		FeeAmountKobo:         t.FeeAmount,
		BankName:              bankName,
		MaskedAccountNumber:   maskedAccount,
		ExpiresAt:             t.ExpiresAt.Format("2006-01-02T15:04:05Z"),
		CreatedAt:             t.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:             t.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		TxHash:                txHash,
		PayoutRef:             payoutRef,
		Confirmations:         t.Confirmations,
		RequiredConfirmations: 2,
	}
	if t.PayoutAuthorizedAt != nil {
		response.PayoutAuthorizedAt = t.PayoutAuthorizedAt.Format("2006-01-02T15:04:05Z")
	}
	return response
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func formatTradeAmount(amount int64, currency string) string {
	decimals := 8
	switch currency {
	case "ETH", "BNB":
		decimals = 18
	case "USDC", "USDT":
		decimals = 6
	}

	divisor := 1.0
	for i := 0; i < decimals; i++ {
		divisor *= 10
	}
	return strconv.FormatFloat(float64(amount)/divisor, 'f', -1, 64)
}
