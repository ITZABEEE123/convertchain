package dto

type CreateTradeRequest struct {
	UserID        string `json:"user_id" binding:"required"`
	QuoteID       string `json:"quote_id" binding:"required"`
	BankAccountID string `json:"bank_account_id" binding:"required"`
}

type TradeResponse struct {
	TradeID        string  `json:"trade_id"`
	UserID         string  `json:"user_id"`
	Status         string  `json:"status"`
	DepositAddress string  `json:"deposit_address"`
	DepositNetwork string  `json:"deposit_network"`
	AmountUSDC     float64 `json:"amount_usdc"`
	AmountNGN      float64 `json:"amount_ngn"`
	Rate           float64 `json:"rate"`
	Fee            float64 `json:"fee_usdc"`
	ExpiresAt      string  `json:"expires_at"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	TxHash         string  `json:"tx_hash,omitempty"`
	PayoutRef      string  `json:"payout_ref,omitempty"`
}