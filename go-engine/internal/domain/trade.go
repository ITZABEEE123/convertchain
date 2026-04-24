// internal/domain/trade.go
package domain

import (
	"time"

	"github.com/google/uuid"
)

// Trade represents a single crypto-to-fiat conversion transaction.
// This is the most important entity in the system — it tracks the entire
// lifecycle from "user wants to sell BTC" to "NGN landed in their bank."
//
// The Trade State Machine controls the "Status" field, ensuring that
// a trade can only move through valid states in the correct order.
type Trade struct {
	ID       uuid.UUID `json:"id" db:"id"`
	TradeRef string    `json:"trade_ref" db:"trade_ref"` // Human-readable: "TRD-A1B2C3D4"

	// Links to other entities
	UserID    uuid.UUID  `json:"user_id" db:"user_id"`
	QuoteID   uuid.UUID  `json:"quote_id" db:"quote_id"`
	BankAccID *uuid.UUID `json:"bank_account_id" db:"bank_account_id"` // NULL until payout stage

	// Current lifecycle state — controlled by the Trade State Machine
	// Valid values: all 13 trade_status enum values from the database
	Status string `json:"status" db:"status"`

	// Currency pair
	FromCurrency string `json:"from_currency" db:"from_currency"` // e.g., "BTC"
	ToCurrency   string `json:"to_currency" db:"to_currency"`     // e.g., "NGN"

	// ──────────────────────────────────────────────
	// MONEY FIELDS — ALL IN MINOR UNITS (BIGINT)
	// BTC: satoshis (1 BTC = 100,000,000)
	// NGN: kobo (₦1 = 100)
	// USD: cents ($1 = 100)
	// NEVER store these as float64
	// ──────────────────────────────────────────────
	FromAmount       int64  `json:"from_amount" db:"from_amount"`               // What user sends (satoshis)
	ToAmountExpected int64  `json:"to_amount_expected" db:"to_amount_expected"` // Expected payout (kobo)
	ToAmountActual   *int64 `json:"to_amount_actual" db:"to_amount_actual"`     // Actual payout (may differ)
	FeeAmount        int64  `json:"fee_amount" db:"fee_amount"`                 // Platform fee (kobo)

	// Crypto deposit tracking
	DepositAddress     *string    `json:"deposit_address" db:"deposit_address"` // Blockchain receive address
	DepositTxHash      *string    `json:"deposit_txhash" db:"deposit_txhash"`   // Blockchain transaction hash
	DepositConfirmedAt *time.Time `json:"deposit_confirmed_at" db:"deposit_confirmed_at"`

	// Exchange execution tracking
	ExchangeOrderID *string `json:"exchange_order_id" db:"exchange_order_id"` // Binance/Bybit order ID

	// Graph Finance tracking
	GraphConversionID *string `json:"graph_conversion_id" db:"graph_conversion_id"` // USDC→NGN conversion
	GraphPayoutID     *string `json:"graph_payout_id" db:"graph_payout_id"`         // Bank payout reference

	// User authorization trail for payout-capable trades.
	PayoutAuthorizedAt        *time.Time `json:"payout_authorized_at" db:"payout_authorized_at"`
	PayoutAuthorizationMethod *string    `json:"payout_authorization_method" db:"payout_authorization_method"`

	// Dispute handling
	DisputeReason *string `json:"dispute_reason" db:"dispute_reason"`

	// Timing
	ExpiresAt   time.Time  `json:"expires_at" db:"expires_at"`     // Deposit window (30 min)
	CompletedAt *time.Time `json:"completed_at" db:"completed_at"` // When fully completed

	// Timestamps
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`

	// ──────────────────────────────────────────────
	// Fields NOT in the database — used only in application logic
	// ──────────────────────────────────────────────

	// ValidUntil comes from the associated Quote, not the trades table.
	// Used by the state machine guard to check if a quote is still valid.
	ValidUntil time.Time `json:"-" db:"-"`

	// Confirmations is the current blockchain confirmation count.
	// Fetched from the blockchain watcher, not stored permanently on this table.
	// Used by the guard that checks "enough confirmations before confirming deposit."
	Confirmations int `json:"-" db:"-"`
}

// TradeReceipt is the user-facing settlement summary for a completed payout.
type TradeReceipt struct {
	TradeID             string
	TradeRef            string
	Status              string
	PricingMode         string
	PayoutAmountKobo    int64
	FeeAmountKobo       int64
	BankName            string
	MaskedAccountNumber string
	PayoutRef           string
	CreatedAt           time.Time
	PayoutCompletedAt   *time.Time
}

// DisputeRecord is the normalized dispute ticket model returned by the service layer.
type DisputeRecord struct {
	ID        string
	TradeID   string
	CreatedAt time.Time
	TicketRef string
}
