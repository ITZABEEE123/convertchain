// internal/domain/kyc.go
package domain

import (
	"time"

	"github.com/google/uuid"
)

// KYCDocument represents a single identity document submitted by a user.
// A user typically has multiple documents: NIN, BVN, selfie, proof of address, etc.
type KYCDocument struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	UserID         uuid.UUID  `json:"user_id" db:"user_id"`
	DocType        string     `json:"doc_type" db:"doc_type"`               // NIN, BVN, SELFIE, etc.
	DocumentNumber *string    `json:"document_number" db:"document_number"` // Encrypted at app level
	FileURL        *string    `json:"file_url" db:"file_url"`
	Provider       *string    `json:"provider" db:"provider"`       // smile_id, sumsub, manual
	ProviderRef    *string    `json:"provider_ref" db:"provider_ref"`
	Verified       *bool      `json:"verified" db:"verified"`
	VerifiedAt     *time.Time `json:"verified_at" db:"verified_at"`
	RejectedReason *string    `json:"rejected_reason" db:"rejected_reason"`
	ExpiresAt      *string    `json:"expires_at" db:"expires_at"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
}

// BankAccount represents a user's linked Nigerian bank account for payouts.
type BankAccount struct {
	ID            uuid.UUID `json:"id" db:"id"`
	UserID        uuid.UUID `json:"user_id" db:"user_id"`
	BankCode      string    `json:"bank_code" db:"bank_code"`           // CBN code: "058"
	AccountNumber string    `json:"account_number" db:"account_number"` // 10-digit NUBAN
	AccountName   string    `json:"account_name" db:"account_name"`     // Verified via NIP
	BankName      *string   `json:"bank_name" db:"bank_name"`
	GraphDestID   *string   `json:"graph_dest_id" db:"graph_dest_id"`
	IsPrimary     bool      `json:"is_primary" db:"is_primary"`
	IsVerified    bool      `json:"is_verified" db:"is_verified"`
	IsActive      bool      `json:"is_active" db:"is_active"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
}

// LedgerEntry represents one side of a double-entry accounting transaction.
type LedgerEntry struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	TradeID        *uuid.UUID `json:"trade_id" db:"trade_id"`
	EntryType      string     `json:"entry_type" db:"entry_type"` // DEPOSIT, FEE, PAYOUT, REFUND
	Currency       string     `json:"currency" db:"currency"`
	Amount         int64      `json:"amount" db:"amount"`             // Always positive
	Direction      string     `json:"direction" db:"direction"`       // "D" or "C"
	AccountRef     string     `json:"account_ref" db:"account_ref"`   // e.g., "platform:fees"
	BalanceAfter   int64      `json:"balance_after" db:"balance_after"`
	IdempotencyKey string     `json:"idempotency_key" db:"idempotency_key"`
	Metadata       *string    `json:"metadata" db:"metadata"` // JSON string
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
}

// TradeStatusHistory records every state change a trade goes through.
type TradeStatusHistory struct {
	ID         uuid.UUID  `json:"id" db:"id"`
	TradeID    uuid.UUID  `json:"trade_id" db:"trade_id"`
	FromStatus *string    `json:"from_status" db:"from_status"` // NULL for first entry
	ToStatus   string     `json:"to_status" db:"to_status"`
	Actor      string     `json:"actor" db:"actor"` // "system", "user", "admin"
	Note       *string    `json:"note" db:"note"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
}

// WebhookEvent represents an inbound webhook from an external service.
type WebhookEvent struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	Source      string     `json:"source" db:"source"`           // binance, graph, smileid
	EventType   string     `json:"event_type" db:"event_type"`
	Payload     string     `json:"payload" db:"payload"`         // JSONB stored as string
	Signature   *string    `json:"signature" db:"signature"`
	Processed   bool       `json:"processed" db:"processed"`
	ProcessedAt *time.Time `json:"processed_at" db:"processed_at"`
	Error       *string    `json:"error" db:"error"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
}