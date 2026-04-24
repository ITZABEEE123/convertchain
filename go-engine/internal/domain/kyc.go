// internal/domain/kyc.go
package domain

import (
	"time"

	"github.com/google/uuid"
)

// KYCDocument represents a single identity document submitted by a user.
type KYCDocument struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	UserID         uuid.UUID  `json:"user_id" db:"user_id"`
	DocType        string     `json:"doc_type" db:"doc_type"`
	DocumentNumber *string    `json:"document_number" db:"document_number"`
	FileURL        *string    `json:"file_url" db:"file_url"`
	Provider       *string    `json:"provider" db:"provider"`
	ProviderRef    *string    `json:"provider_ref" db:"provider_ref"`
	Verified       *bool      `json:"verified" db:"verified"`
	VerifiedAt     *time.Time `json:"verified_at" db:"verified_at"`
	RejectedReason *string    `json:"rejected_reason" db:"rejected_reason"`
	ExpiresAt      *string    `json:"expires_at" db:"expires_at"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
}

// KYCStatusSummary is the normalized KYC state returned to the API layer.
type KYCStatusSummary struct {
	UserID                 uuid.UUID  `json:"user_id"`
	Status                 string     `json:"status"`
	Tier                   string     `json:"tier"`
	Provider               string     `json:"provider,omitempty"`
	ProviderRef            string     `json:"provider_ref,omitempty"`
	SubmittedAt            *time.Time `json:"submitted_at,omitempty"`
	CompletedAt            *time.Time `json:"completed_at,omitempty"`
	RejectionReason        string     `json:"rejection_reason,omitempty"`
	TransactionPasswordSet bool       `json:"transaction_password_set"`
}

// BankAccount represents a user's linked Nigerian bank account for payouts.
type BankAccount struct {
	ID            uuid.UUID `json:"id" db:"id"`
	UserID        uuid.UUID `json:"user_id" db:"user_id"`
	BankCode      string    `json:"bank_code" db:"bank_code"`
	AccountNumber string    `json:"account_number" db:"account_number"`
	AccountName   string    `json:"account_name" db:"account_name"`
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
	EntryType      string     `json:"entry_type" db:"entry_type"`
	Currency       string     `json:"currency" db:"currency"`
	Amount         int64      `json:"amount" db:"amount"`
	Direction      string     `json:"direction" db:"direction"`
	AccountRef     string     `json:"account_ref" db:"account_ref"`
	BalanceAfter   int64      `json:"balance_after" db:"balance_after"`
	IdempotencyKey string     `json:"idempotency_key" db:"idempotency_key"`
	Metadata       *string    `json:"metadata" db:"metadata"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
}

// TradeStatusHistory records every state change a trade goes through.
type TradeStatusHistory struct {
	ID         uuid.UUID `json:"id" db:"id"`
	TradeID    uuid.UUID `json:"trade_id" db:"trade_id"`
	FromStatus *string   `json:"from_status" db:"from_status"`
	ToStatus   string    `json:"to_status" db:"to_status"`
	Actor      string    `json:"actor" db:"actor"`
	Note       *string   `json:"note" db:"note"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
}

// WebhookEvent represents an inbound webhook from an external service.
type WebhookEvent struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	Source      string     `json:"source" db:"source"`
	EventType   string     `json:"event_type" db:"event_type"`
	Payload     string     `json:"payload" db:"payload"`
	Signature   *string    `json:"signature" db:"signature"`
	Processed   bool       `json:"processed" db:"processed"`
	ProcessedAt *time.Time `json:"processed_at" db:"processed_at"`
	Error       *string    `json:"error" db:"error"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
}

// NotificationEvent is a delivery outbox row produced by the Go engine and
// consumed by the Python Telegram runtime.
type NotificationEvent struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	UserID        uuid.UUID  `json:"user_id" db:"user_id"`
	ChannelType   string     `json:"channel_type" db:"channel_type"`
	TradeID       *uuid.UUID `json:"trade_id" db:"trade_id"`
	EventType     string     `json:"event_type" db:"event_type"`
	Payload       string     `json:"payload" db:"payload"`
	DedupeKey     string     `json:"dedupe_key" db:"dedupe_key"`
	Delivered     bool       `json:"delivered" db:"delivered"`
	DeliveredAt   *time.Time `json:"delivered_at" db:"delivered_at"`
	DeliveryError *string    `json:"delivery_error" db:"delivery_error"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
}

// PendingNotification is the denormalized outbox item handed to the messaging layer.
type PendingNotification struct {
	ID          uuid.UUID              `json:"id"`
	ChannelType string                 `json:"channel_type"`
	RecipientID string                 `json:"recipient_id"`
	TradeID     string                 `json:"trade_id,omitempty"`
	EventType   string                 `json:"event_type"`
	Payload     map[string]interface{} `json:"payload"`
	ClaimToken  string                 `json:"claim_token,omitempty"`
	Attempts    int                    `json:"attempts"`
	CreatedAt   time.Time              `json:"created_at"`
}
