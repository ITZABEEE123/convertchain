package domain

import (
	"time"

	"github.com/google/uuid"
)

type AMLReviewCase struct {
	ID                  uuid.UUID
	CaseRef             string
	UserID              *uuid.UUID
	TradeID             *uuid.UUID
	QuoteID             *uuid.UUID
	CaseType            string
	Severity            string
	Status              string
	Reason              string
	Evidence            map[string]interface{}
	Disposition         *string
	DispositionNote     *string
	STRReferralMetadata map[string]interface{}
	AssignedTo          *string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	ResolvedAt          *time.Time
}

type ComplianceScreeningEvent struct {
	ID               uuid.UUID
	UserID           *uuid.UUID
	TradeID          *uuid.UUID
	QuoteID          *uuid.UUID
	ScreeningScope   string
	ScreeningType    string
	ScreeningSubject string
	Decision         string
	DecisionReason   *string
	ProviderRef      *string
	Metadata         map[string]interface{}
	CreatedAt        time.Time
}

type LegalLaunchApproval struct {
	ID             uuid.UUID
	Environment    string
	ApprovalStatus string
	ApprovedBy     *string
	LegalMemoRef   *string
	Notes          *string
	Evidence       map[string]interface{}
	SignedAt       *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type DataProtectionEvent struct {
	ID        uuid.UUID
	UserID    *uuid.UUID
	EventType string
	Status    string
	Reference *string
	Details   map[string]interface{}
	CreatedBy *string
	CreatedAt time.Time
	UpdatedAt time.Time
}
