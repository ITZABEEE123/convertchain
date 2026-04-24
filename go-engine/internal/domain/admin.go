package domain

import (
	"time"

	"github.com/google/uuid"
)

type TradeDispute struct {
	ID             uuid.UUID
	TicketRef      string
	TradeID        uuid.UUID
	TradeRef       string
	UserID         uuid.UUID
	Source         string
	Status         string
	Reason         string
	ResolutionMode *string
	ResolutionNote *string
	Resolver       *string
	CreatedAt      time.Time
	ResolvedAt     *time.Time
	UpdatedAt      time.Time
}

type ProviderReadinessCheck struct {
	Enabled bool
	Healthy bool
	Summary string
	Details map[string]interface{}
}

type ProviderReadinessReport struct {
	GeneratedAt    time.Time
	OverallHealthy bool
	Graph          ProviderReadinessCheck
	Binance        ProviderReadinessCheck
	Bybit          ProviderReadinessCheck
	SmileID        ProviderReadinessCheck
	Sumsub         ProviderReadinessCheck
}

type TradeStatusContext struct {
	ContextType string
	Trade       *Trade
	Receipt     *TradeReceipt
	Dispute     *TradeDispute
}
