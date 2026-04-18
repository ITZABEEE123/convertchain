package dto

type CreateTradeRequest struct {
	UserID        string `json:"user_id" binding:"required"`
	QuoteID       string `json:"quote_id" binding:"required"`
	BankAccountID string `json:"bank_account_id" binding:"required"`
}

type TradeResponse struct {
	TradeID               string `json:"trade_id"`
	UserID                string `json:"user_id"`
	Status                string `json:"status"`
	DepositAddress        string `json:"deposit_address,omitempty"`
	DepositAmount         string `json:"deposit_amount,omitempty"`
	Asset                 string `json:"asset,omitempty"`
	ExpiresAt             string `json:"expires_at,omitempty"`
	CreatedAt             string `json:"created_at,omitempty"`
	UpdatedAt             string `json:"updated_at,omitempty"`
	TxHash                string `json:"tx_hash,omitempty"`
	PayoutRef             string `json:"payout_ref,omitempty"`
	Confirmations         int    `json:"confirmations,omitempty"`
	RequiredConfirmations int    `json:"required_confirmations,omitempty"`
}
