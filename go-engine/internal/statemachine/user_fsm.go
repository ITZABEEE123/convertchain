// internal/statemachine/user_fsm.go
//
// This file implements a Finite State Machine (FSM) that controls
// how a user progresses through the registration and KYC process.
//
// The FSM guarantees that users can ONLY move through valid state
// transitions. For example, a user cannot jump from UNREGISTERED
// directly to KYC_APPROVED — they must go through KYC_IN_PROGRESS
// and KYC_PENDING first.
//
// This is critical for compliance: regulators need proof that every
// user went through proper verification before being allowed to trade.
package statemachine

import (
	"context"
	"fmt"
	"time"

	// This import path must match YOUR go.mod module name.
	// The handbook uses "github.com/openclaw/engine" but yours is
	// "convert-chain/go-engine". Adjust accordingly.
	"convert-chain/go-engine/internal/domain"
)

// ──────────────────────────────────────────────
// STATE DEFINITIONS
// These constants match the user_status ENUM in PostgreSQL exactly.
// If you add a state here, you must also add it to the database enum.
// ──────────────────────────────────────────────

// UserState represents the possible registration states of a user.
// It's a custom type based on string, which gives us type safety —
// you can't accidentally pass a TradeState where a UserState is expected.
type UserState string

const (
	// StateUnregistered — The user has sent their first message on
	// WhatsApp/Telegram but hasn't provided any information yet.
	// This is the starting state for every new user.
	StateUnregistered UserState = "UNREGISTERED"

	// StateKYCInProgress — The user has given consent and is actively
	// submitting their identity documents (NIN, BVN, etc.).
	StateKYCInProgress UserState = "KYC_IN_PROGRESS"

	// StateKYCPending — All documents have been submitted and are
	// waiting for verification by SmileID or Sumsub.
	StateKYCPending UserState = "KYC_PENDING"

	// StateKYCApproved — Identity verified successfully. The user
	// can now trade within their tier's limits.
	StateKYCApproved UserState = "KYC_APPROVED"

	// StateKYCRejected — Verification failed. The user can retry
	// after a 24-hour cooling period.
	StateKYCRejected UserState = "KYC_REJECTED"
)

// ──────────────────────────────────────────────
// EVENT DEFINITIONS
// Events are "things that happen" which trigger state transitions.
// An event + current state = new state (if the transition is valid).
// ──────────────────────────────────────────────

// UserEvent represents actions or occurrences that can change a user's state.
type UserEvent string

const (
	// EventConsentGiven — The user typed "YES" to the consent prompt.
	// Required by Nigerian data protection regulations (NDPR/NDPA).
	EventConsentGiven UserEvent = "CONSENT_GIVEN"

	// EventKYCSubmitted — The user has finished submitting all required
	// documents for their current tier level.
	EventKYCSubmitted UserEvent = "KYC_SUBMITTED"

	// EventKYCApproved — The verification provider (SmileID/Sumsub)
	// confirmed that the documents are valid.
	EventKYCApproved UserEvent = "KYC_APPROVED"

	// EventKYCRejected — The verification provider found issues
	// (name mismatch, expired document, failed liveness, etc.).
	EventKYCRejected UserEvent = "KYC_REJECTED"

	// EventKYCRetry — The user is attempting to re-submit after rejection.
	// Only allowed after a 24-hour cooling period.
	EventKYCRetry UserEvent = "KYC_RETRY"
)

// ──────────────────────────────────────────────
// TRANSITION DEFINITION
// A transition defines: "If you're in state X and event Y happens,
// move to state Z — but only if the guard condition passes."
// ──────────────────────────────────────────────

// UserTransition defines a single valid state change.
type UserTransition struct {
	// From — the state the user must currently be in
	From UserState

	// Event — what happened that might trigger a transition
	Event UserEvent

	// To — the state the user will move to (if the guard passes)
	To UserState

	// Guard — an optional validation function that runs BEFORE the transition.
	// If the guard returns an error, the transition is BLOCKED.
	// If Guard is nil, the transition always proceeds.
	//
	// Guards enforce business rules like:
	// - "consent timestamp must be present before moving to KYC"
	// - "phone number required before submitting documents"
	// - "24-hour cooldown before retrying after rejection"
	Guard func(u *domain.User) error

	// Action — an optional function that runs AFTER the transition succeeds.
	// Used for side effects like sending notifications, updating external
	// systems, or logging to the audit trail.
	// If Action is nil, nothing extra happens after the state change.
	Action func(ctx context.Context, u *domain.User) error
}

// ──────────────────────────────────────────────
// TRANSITION TABLE
// This is the "rule book" — every valid transition is listed here.
// If a From+Event combination is NOT in this list, it's FORBIDDEN.
// ──────────────────────────────────────────────

var userTransitions = []UserTransition{
	// ── Transition 1: Start KYC Process ──
	// User gives consent → can begin submitting documents
	{
		From:  StateUnregistered,
		Event: EventConsentGiven,
		To:    StateKYCInProgress,
		Guard: func(u *domain.User) error {
			// GUARD: Consent timestamp must be recorded.
			// The system should have set ConsentGivenAt when the user
			// typed "YES" on WhatsApp/Telegram. If it's nil, something
			// went wrong in the consent collection flow.
			if u.ConsentGivenAt == nil {
				return fmt.Errorf("consent timestamp required")
			}
			return nil // Guard passes — transition allowed
		},
		// No Action needed — just the state change
	},

	// ── Transition 2: Submit Documents ──
	// User finishes entering BVN, NIN, etc. → documents sent for review
	{
		From:  StateKYCInProgress,
		Event: EventKYCSubmitted,
		To:    StateKYCPending,
		Guard: func(u *domain.User) error {
			// GUARD: Phone number must be collected before submission.
			// This is a minimum data requirement — the KYC provider
			// needs a phone number to verify against BVN records.
			if u.PhoneNumber == "" {
				return fmt.Errorf("phone number required for KYC submission")
			}
			return nil
		},
	},

	// ── Transition 3: KYC Approved ──
	// Verification provider confirms documents are valid
	{
		From:  StateKYCPending,
		Event: EventKYCApproved,
		To:    StateKYCApproved,
		// No guard — if the provider says approved, we accept it.
		// The actual verification logic is in the KYC orchestrator,
		// not in the state machine. The FSM just records the result.
	},

	// ── Transition 4: KYC Rejected ──
	// Verification provider found issues with the documents
	{
		From:  StateKYCPending,
		Event: EventKYCRejected,
		To:    StateKYCRejected,
		// No guard — if the provider says rejected, we accept it.
	},

	// ── Transition 5: Retry After Rejection ──
	// User wants to try again after being rejected
	{
		From:  StateKYCRejected,
		Event: EventKYCRetry,
		To:    StateKYCInProgress, // Goes back to document collection
		Guard: func(u *domain.User) error {
			// GUARD: 24-hour cooling period after rejection.
			//
			// Why? Two reasons:
			// 1. Prevents rapid-fire retry abuse (someone trying different
			//    fake documents over and over)
			// 2. Gives the user time to gather correct documents
			//
			// LastRejectedAt is populated by querying the most recent
			// rejected KYC document from the kyc_documents table.
			if u.LastRejectedAt != nil {
				timeSinceRejection := time.Since(*u.LastRejectedAt)
				if timeSinceRejection < 24*time.Hour {
					hoursRemaining := int((24*time.Hour - timeSinceRejection).Hours())
					return fmt.Errorf(
						"KYC retry not yet allowed — please wait %d more hours", hoursRemaining,
					)
				}
			}
			return nil
		},
	},
}

// ──────────────────────────────────────────────
// THE FSM ENGINE
// This is the actual state machine that uses the transition table.
// ──────────────────────────────────────────────

// UserFSM is the User Finite State Machine.
// It holds a pre-built lookup map for fast transition lookups:
//   transitions[currentState][event] → transition rule
type UserFSM struct {
	transitions map[UserState]map[UserEvent]UserTransition
}

// NewUserFSM creates and initializes the User FSM.
// It converts the flat transition list into a nested map for O(1) lookups.
//
// Without the map, finding a transition would require looping through
// all transitions every time (O(n)). The map makes it instant.
func NewUserFSM() *UserFSM {
	fsm := &UserFSM{
		transitions: make(map[UserState]map[UserEvent]UserTransition),
	}

	// Build the lookup map from the transition list
	for _, t := range userTransitions {
		// If this is the first transition from this state, create the inner map
		if fsm.transitions[t.From] == nil {
			fsm.transitions[t.From] = make(map[UserEvent]UserTransition)
		}
		// Register: "from state X, event Y → transition rule"
		fsm.transitions[t.From][t.Event] = t
	}

	return fsm
}

// Transition attempts to move a user from their current state to a new state.
//
// It performs three checks in order:
//   1. Does any transition exist from the user's current state?
//   2. Does the specific event have a valid transition from this state?
//   3. Does the guard condition (if any) pass?
//
// If all checks pass, the user's Status field is updated.
// If an Action function is defined, it runs after the state change.
//
// Parameters:
//   - ctx: context for cancellation and timeouts
//   - u: the user to transition (modified in place)
//   - event: what happened that should trigger the transition
//
// Returns:
//   - nil if the transition succeeded
//   - error if the transition was invalid or the guard failed
func (f *UserFSM) Transition(ctx context.Context, u *domain.User, event UserEvent) error {
	// Step 1: Look up all possible transitions from the user's current state
	stateMap, ok := f.transitions[UserState(u.Status)]
	if !ok {
		// No transitions defined from this state at all.
		// Example: trying to transition from KYC_APPROVED (it's a terminal state
		// for happy-path flow — no events move you out of approved).
		return fmt.Errorf("no transitions defined from state %s", u.Status)
	}

	// Step 2: Look up the specific transition for this event
	t, ok := stateMap[event]
	if !ok {
		// This specific event is not valid from this state.
		// Example: trying to fire KYC_RETRY while in KYC_IN_PROGRESS
		// (retry only works from KYC_REJECTED).
		return fmt.Errorf("event %s is not valid in state %s", event, u.Status)
	}

	// Step 3: Run the guard (if one exists)
	if t.Guard != nil {
		if err := t.Guard(u); err != nil {
			// Guard failed — transition is blocked.
			// Example: trying to retry KYC within the 24-hour cooldown.
			return fmt.Errorf("transition blocked: %w", err)
		}
	}

	// Step 4: Apply the state change
	// This modifies the user struct in memory. The caller is responsible
	// for saving this change to the database.
	u.Status = string(t.To)

	// Step 5: Run the action (if one exists)
	if t.Action != nil {
		if err := t.Action(ctx, u); err != nil {
			// Action failed — but the state has already changed in memory.
			// The caller should handle this (e.g., by not saving to DB).
			return fmt.Errorf("post-transition action failed: %w", err)
		}
	}

	return nil // Success — user has been transitioned
}