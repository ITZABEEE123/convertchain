package dto

type DisputeRequest struct {
	UserID      string `json:"user_id" binding:"required"`
	TradeID     string `json:"trade_id" binding:"required"`
	Reason      string `json:"reason" binding:"required,oneof=DEPOSIT_NOT_CREDITED WRONG_AMOUNT PAYOUT_DELAYED OTHER"`
	Description string `json:"description"`
}

type DisputeResponse struct {
	DisputeID string `json:"dispute_id"`
	TradeID   string `json:"trade_id"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	TicketRef string `json:"ticket_ref"`
}