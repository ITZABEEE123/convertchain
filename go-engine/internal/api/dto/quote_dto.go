package dto

type QuoteRequest struct {
	UserID     string  `json:"user_id" binding:"required"`
	AmountUSDC float64 `json:"amount_usdc" binding:"required,gt=0"`
}

type QuoteResponse struct {
	QuoteID    string  `json:"quote_id"`
	AmountUSDC float64 `json:"amount_usdc"`
	AmountNGN  float64 `json:"amount_ngn"`
	Rate       float64 `json:"rate"`
	Fee        float64 `json:"fee_usdc"`
	ExpiresAt  string  `json:"expires_at"`
	Status     string  `json:"status"`
}