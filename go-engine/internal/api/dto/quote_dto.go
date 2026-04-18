package dto

type QuoteRequest struct {
	UserID    string `json:"user_id" binding:"required"`
	Asset     string `json:"asset" binding:"required,oneof=BTC ETH USDT USDC BNB btc eth usdt usdc bnb"`
	Amount    string `json:"amount" binding:"required"`
	Direction string `json:"direction" binding:"omitempty,oneof=sell SELL"`
}

type QuoteResponse struct {
	QuoteID      string `json:"quote_id"`
	Asset        string `json:"asset"`
	Amount       string `json:"amount"`
	Rate         int64  `json:"rate"`
	FeeKobo      int64  `json:"fee_kobo"`
	NetNairaKobo int64  `json:"net_naira_kobo"`
	ExpiresAt    string `json:"expires_at"`
	Status       string `json:"status"`
}
