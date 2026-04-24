package dto

type AdminDisputeResponse struct {
	DisputeID      string `json:"dispute_id"`
	TicketRef      string `json:"ticket_ref"`
	TradeID        string `json:"trade_id"`
	TradeRef       string `json:"trade_ref"`
	UserID         string `json:"user_id"`
	Source         string `json:"source"`
	Status         string `json:"status"`
	Reason         string `json:"reason"`
	ResolutionMode string `json:"resolution_mode,omitempty"`
	ResolutionNote string `json:"resolution_note,omitempty"`
	Resolver       string `json:"resolver,omitempty"`
	CreatedAt      string `json:"created_at"`
	ResolvedAt     string `json:"resolved_at,omitempty"`
	UpdatedAt      string `json:"updated_at"`
}

type AdminDisputeListResponse struct {
	Disputes []AdminDisputeResponse `json:"disputes"`
}

type ResolveDisputeRequest struct {
	ResolutionMode string `json:"resolution_mode" binding:"required,oneof=retry_processing close_no_payout force_complete"`
	ResolutionNote string `json:"resolution_note"`
	Resolver       string `json:"resolver"`
}

type ProviderReadinessCheckResponse struct {
	Enabled bool                   `json:"enabled"`
	Healthy bool                   `json:"healthy"`
	Summary string                 `json:"summary"`
	Details map[string]interface{} `json:"details,omitempty"`
}

type ProviderReadinessResponse struct {
	GeneratedAt    string                         `json:"generated_at"`
	OverallHealthy bool                           `json:"overall_healthy"`
	Graph          ProviderReadinessCheckResponse `json:"graph"`
	Binance        ProviderReadinessCheckResponse `json:"binance"`
	Bybit          ProviderReadinessCheckResponse `json:"bybit"`
	SmileID        ProviderReadinessCheckResponse `json:"smileid"`
	Sumsub         ProviderReadinessCheckResponse `json:"sumsub"`
}
