package dto

type NotificationEnvelope struct {
	ID          string                 `json:"id"`
	ChannelType string                 `json:"channel_type"`
	RecipientID string                 `json:"recipient_id"`
	TradeID     string                 `json:"trade_id,omitempty"`
	EventType   string                 `json:"event_type"`
	Payload     map[string]interface{} `json:"payload"`
	ClaimToken  string                 `json:"claim_token,omitempty"`
	Attempts    int                    `json:"attempts"`
	CreatedAt   string                 `json:"created_at"`
}

type PendingNotificationsResponse struct {
	Notifications []NotificationEnvelope `json:"notifications"`
}

type NotificationAckRequest struct {
	Delivered     bool   `json:"delivered"`
	DeliveryError string `json:"delivery_error"`
	ClaimToken    string `json:"claim_token"`
}

type NotificationMetricsResponse struct {
	ChannelType string `json:"channel_type"`
	Pending     int    `json:"pending"`
	DeadLetter  int    `json:"dead_letter"`
}
