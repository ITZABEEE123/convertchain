package handlers

import (
	"context"
	"net/http"
	"strconv"

	"convert-chain/go-engine/internal/api/dto"
	"convert-chain/go-engine/internal/domain"
	"github.com/gin-gonic/gin"
)

type QuoteService interface {
	CreateQuote(ctx context.Context, req dto.QuoteRequest) (*domain.Quote, error)
	GetUserKYCStatus(ctx context.Context, userID string) (string, error)
}

type QuoteHandler struct {
	svc QuoteService
}

func NewQuoteHandler(svc QuoteService) *QuoteHandler {
	return &QuoteHandler{svc: svc}
}

// CreateQuote handles POST /api/v1/quotes.
// Requires KYC_APPROVED status. Rate limited to 10 quotes per minute.
func (h *QuoteHandler) CreateQuote(c *gin.Context) {
	var req dto.QuoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.NewError(dto.ErrCodeValidation, "Invalid request body", err.Error()))
		return
	}

	kycStatus, err := h.svc.GetUserKYCStatus(c.Request.Context(), req.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to verify KYC status", nil))
		return
	}

	if kycStatus != "APPROVED" {
		c.JSON(http.StatusForbidden, dto.NewError(
			dto.ErrCodeKYCNotApproved,
			"Your identity verification (KYC) must be completed before you can request quotes. Current status: "+kycStatus,
			nil,
		))
		return
	}

	quote, err := h.svc.CreateQuote(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, dto.NewError(dto.ErrCodeInternalError, "Failed to generate quote", nil))
		return
	}

	c.JSON(http.StatusCreated, dto.QuoteResponse{
		QuoteID:               quote.ID.String(),
		Asset:                 quote.FromCurrency,
		Amount:                formatAmountForClient(quote.FromAmount, quote.FromCurrency),
		Rate:                  quote.UserRatePerUnitKobo,
		FeeKobo:               quote.FeeAmount,
		NetNairaKobo:          quote.NetAmount,
		GrossNairaKobo:        quote.ToAmount,
		PlatformFeeKobo:       quote.FeeAmount,
		PlatformFeeBPS:        quote.FeeBPS,
		MarketRatePerUnitKobo: quote.MarketRatePerUnitKobo,
		UserRatePerUnitKobo:   quote.UserRatePerUnitKobo,
		PricingMode:           quote.PricingMode,
		PriceSource:           quote.PriceSource,
		FiatRateSource:        quote.FiatRateSource,
		ExpiresAt:             quote.ValidUntil.Format("2006-01-02T15:04:05Z"),
		Status:                deriveQuoteStatus(quote),
	})
}

func deriveQuoteStatus(quote *domain.Quote) string {
	if quote.ExpiredAt != nil {
		return "QUOTE_EXPIRED"
	}
	if quote.AcceptedAt != nil {
		return "QUOTE_ACCEPTED"
	}
	return "QUOTE_PROVIDED"
}

func formatAmountForClient(amount int64, currency string) string {
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
