package dto

type CreateTradeRequest struct {
	UserID        string `json:"user_id" binding:"required"`
	QuoteID       string `json:"quote_id" binding:"required"`
	BankAccountID string `json:"bank_account_id" binding:"required"`
}

type ConfirmTradeRequest struct {
	UserID              string `json:"user_id" binding:"required"`
	QuoteID             string `json:"quote_id" binding:"required"`
	BankAccountID       string `json:"bank_account_id" binding:"required"`
	TransactionPassword string `json:"transaction_password" binding:"required"`
}

type TradeResponse struct {
	TradeID               string `json:"trade_id"`
	TradeRef              string `json:"trade_ref,omitempty"`
	UserID                string `json:"user_id"`
	Status                string `json:"status"`
	DisputeReason         string `json:"dispute_reason,omitempty"`
	DepositAddress        string `json:"deposit_address,omitempty"`
	DepositAmount         string `json:"deposit_amount,omitempty"`
	Asset                 string `json:"asset,omitempty"`
	NetAmountKobo         int64  `json:"net_amount_kobo,omitempty"`
	FeeAmountKobo         int64  `json:"fee_amount_kobo,omitempty"`
	BankName              string `json:"bank_name,omitempty"`
	MaskedAccountNumber   string `json:"masked_account_number,omitempty"`
	PayoutAuthorizedAt    string `json:"payout_authorized_at,omitempty"`
	ExpiresAt             string `json:"expires_at,omitempty"`
	CreatedAt             string `json:"created_at,omitempty"`
	UpdatedAt             string `json:"updated_at,omitempty"`
	TxHash                string `json:"tx_hash,omitempty"`
	PayoutRef             string `json:"payout_ref,omitempty"`
	Confirmations         int    `json:"confirmations,omitempty"`
	RequiredConfirmations int    `json:"required_confirmations,omitempty"`
}

type TradeReceiptResponse struct {
	TradeID             string `json:"trade_id"`
	TradeRef            string `json:"trade_ref"`
	Status              string `json:"status"`
	PricingMode         string `json:"pricing_mode,omitempty"`
	PayoutAmountKobo    int64  `json:"payout_amount_kobo"`
	FeeAmountKobo       int64  `json:"fee_amount_kobo"`
	BankName            string `json:"bank_name,omitempty"`
	MaskedAccountNumber string `json:"masked_account_number,omitempty"`
	PayoutRef           string `json:"payout_ref,omitempty"`
	CreatedAt           string `json:"created_at,omitempty"`
	PayoutCompletedAt   string `json:"payout_completed_at,omitempty"`
}

type TradeDisputeStatusResponse struct {
	DisputeID      string `json:"dispute_id"`
	TicketRef      string `json:"ticket_ref"`
	Status         string `json:"status"`
	Source         string `json:"source"`
	Reason         string `json:"reason"`
	ResolutionMode string `json:"resolution_mode,omitempty"`
	ResolutionNote string `json:"resolution_note,omitempty"`
	ResolvedAt     string `json:"resolved_at,omitempty"`
}

type TradeStatusContextResponse struct {
	ContextType    string                     `json:"context_type"`
	HasActiveTrade bool                       `json:"has_active_trade"`
	Trade          *TradeResponse             `json:"trade,omitempty"`
	Receipt        *TradeReceiptResponse      `json:"receipt,omitempty"`
	Dispute        *TradeDisputeStatusResponse `json:"dispute,omitempty"`
}
