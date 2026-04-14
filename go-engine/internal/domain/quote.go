// internal/domain/quote.go
package domain

import (
	"time"

	"github.com/google/uuid"
)

// Quote represents a price offer shown to a user.
// A quote is valid for 120 seconds. If the user accepts it within that
// window, it becomes a Trade. Otherwise, it expires.
type Quote struct {
	ID uuid.UUID `json:"id" db:"id"`

	UserID       uuid.UUID `json:"user_id" db:"user_id"`
	FromCurrency string    `json:"from_currency" db:"from_currency"`
	ToCurrency   string    `json:"to_currency" db:"to_currency"`

	// All amounts in minor units (satoshis, kobo, cents)
	FromAmount int64 `json:"from_amount" db:"from_amount"` // What user is selling
	ToAmount   int64 `json:"to_amount" db:"to_amount"`     // Gross amount before fee
	NetAmount  int64 `json:"net_amount" db:"net_amount"`    // What user actually receives

	// Rates at quote time
	ExchangeRate string `json:"exchange_rate" db:"exchange_rate"` // BTC/USDC
	FiatRate     string `json:"fiat_rate" db:"fiat_rate"`         // USDC/NGN

	// Fee
	FeeBPS    int   `json:"fee_bps" db:"fee_bps"`       // Basis points (200 = 2%)
	FeeAmount int64 `json:"fee_amount" db:"fee_amount"` // Fee in to_currency minor units

	// Lifecycle
	ValidUntil time.Time  `json:"valid_until" db:"valid_until"`
	AcceptedAt *time.Time `json:"accepted_at" db:"accepted_at"`
	ExpiredAt  *time.Time `json:"expired_at" db:"expired_at"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
}