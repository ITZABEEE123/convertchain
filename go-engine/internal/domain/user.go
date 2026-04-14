// internal/domain/user.go
package domain

import (
	"time"

	"github.com/google/uuid"
)

// User represents a platform user. This struct mirrors the "users" table
// in PostgreSQL. Every field here corresponds to a column in that table.
//
// When the Python bot sends a message saying "a new WhatsApp user just said hi",
// the Go engine creates a User struct, populates it, and saves it to the database.
type User struct {
	// ID is a UUID primary key, generated automatically by PostgreSQL.
	// Example: "550e8400-e29b-41d4-a716-446655440000"
	ID uuid.UUID `json:"id" db:"id"`

	// ChannelType is WHERE the user came from: "WHATSAPP" or "TELEGRAM"
	ChannelType string `json:"channel_type" db:"channel_type"`

	// ChannelUserID is their unique identifier on that platform.
	// WhatsApp: their phone number like "2348012345678"
	// Telegram: their chat_id like "123456789"
	ChannelUserID string `json:"channel_user_id" db:"channel_user_id"`

	// Personal information (collected during KYC onboarding)
	PhoneNumber string  `json:"phone_number" db:"phone_number"`
	Email       string  `json:"email" db:"email"`
	FirstName   string  `json:"first_name" db:"first_name"`
	LastName    string  `json:"last_name" db:"last_name"`
	DateOfBirth *string `json:"date_of_birth" db:"date_of_birth"` // pointer because it can be NULL

	// Status tracks where the user is in the registration process.
	// This is the field that the User State Machine controls.
	// Valid values: UNREGISTERED, KYC_IN_PROGRESS, KYC_PENDING, KYC_APPROVED, KYC_REJECTED
	Status string `json:"status" db:"status"`

	// KYCTier determines the user's transaction limits.
	// TIER_0 = unverified, TIER_1 = basic, up to TIER_4 = business
	KYCTier string `json:"kyc_tier" db:"kyc_tier"`

	// GraphPersonID is the user's ID in Graph Finance's system.
	// Created when KYC is approved and the user is registered for payouts.
	GraphPersonID *string `json:"graph_person_id" db:"graph_person_id"`

	// Consent tracking — legal requirement in Nigeria
	ConsentGivenAt *time.Time `json:"consent_given_at" db:"consent_given_at"`
	ConsentIP      *string    `json:"consent_ip" db:"consent_ip"`

	// Soft delete flag — TRUE means active, FALSE means deactivated
	IsActive bool `json:"is_active" db:"is_active"`

	// Timestamps
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`

	// ──────────────────────────────────────────────
	// Fields NOT in the database — used only in application logic
	// ──────────────────────────────────────────────

	// LastRejectedAt tracks when KYC was last rejected.
	// Used by the state machine's guard to enforce the 24-hour retry cooldown.
	// This is populated by querying kyc_documents, not stored on the users table.
	LastRejectedAt *time.Time `json:"-" db:"-"`
}