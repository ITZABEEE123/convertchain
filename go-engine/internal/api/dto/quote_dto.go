package dto

type QuoteRequest struct {
	UserID    string `json:"user_id" binding:"required"`
	Asset     string `json:"asset" binding:"required,oneof=BTC ETH USDT USDC BNB btc eth usdt usdc bnb"`
	Amount    string `json:"amount" binding:"required"`
	Direction string `json:"direction" binding:"omitempty,oneof=sell SELL"`
}

type QuoteResponse struct {
	QuoteID               string `json:"quote_id"`
	Asset                 string `json:"asset"`
	Amount                string `json:"amount"`
	Rate                  int64  `json:"rate"` // legacy compatibility
	FeeKobo               int64  `json:"fee_kobo"`
	NetNairaKobo          int64  `json:"net_naira_kobo"`
	GrossNairaKobo        int64  `json:"gross_naira_kobo"`
	PlatformFeeKobo       int64  `json:"platform_fee_kobo"`
	PlatformFeeBPS        int    `json:"platform_fee_bps"`
	MarketRatePerUnitKobo int64  `json:"market_rate_per_unit_kobo"`
	UserRatePerUnitKobo   int64  `json:"user_rate_per_unit_kobo"`
	PricingMode           string `json:"pricing_mode"`
	PriceSource           string `json:"price_source"`
	FiatRateSource        string `json:"fiat_rate_source"`
	ExpiresAt             string `json:"expires_at"`
	Status                string `json:"status"`
}
