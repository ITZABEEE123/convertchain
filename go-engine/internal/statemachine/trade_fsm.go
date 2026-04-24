// internal/statemachine/trade_fsm.go
//
// This file implements the Trade Finite State Machine — the most critical
// piece of business logic in the entire platform. It controls the lifecycle
// of every crypto-to-fiat trade from quote to bank payout.
//
// There are 13 possible states and 14 transitions. Every trade must follow
// a valid path through these states. The FSM makes it physically impossible
// for a trade to skip steps or end up in an inconsistent state.
//
// Why this matters: Without the FSM, a bug could cause a trade to jump
// from QUOTE_PROVIDED to PAYOUT_COMPLETED — meaning we'd pay someone
// without ever receiving their crypto. The FSM prevents this by design.
package statemachine

import (
	"context"
	"fmt"
	"time"

	"convert-chain/go-engine/internal/domain"
)

// ──────────────────────────────────────────────
// TRADE STATE DEFINITIONS
// These match the trade_status ENUM in PostgreSQL exactly.
// ──────────────────────────────────────────────

type TradeState string

const (
	// Phase 1: Quote
	TradeQuoteProvided TradeState = "QUOTE_PROVIDED" // Price shown to user
	TradeQuoteExpired  TradeState = "QUOTE_EXPIRED"  // User didn't accept in time

	// Phase 2: Deposit
	TradeCreated         TradeState = "TRADE_CREATED"     // User accepted quote
	TradePendingDeposit  TradeState = "PENDING_DEPOSIT"   // Waiting for crypto
	TradeDepositReceived TradeState = "DEPOSIT_RECEIVED"  // Saw tx on blockchain (unconfirmed)
	TradeDepositConfirmed TradeState = "DEPOSIT_CONFIRMED" // Enough blockchain confirmations

	// Phase 3: Conversion
	TradeConversionInProgress TradeState = "CONVERSION_IN_PROGRESS" // Selling on exchange
	TradeConversionCompleted  TradeState = "CONVERSION_COMPLETED"   // Exchange order filled

	// Phase 4: Payout
	TradePayoutPending   TradeState = "PAYOUT_PENDING"   // NGN payout initiated
	TradePayoutCompleted TradeState = "PAYOUT_COMPLETED" // Money in user's bank
	TradePayoutFailed    TradeState = "PAYOUT_FAILED"    // Bank payout failed

	// Phase 5: Exceptions
	TradeDispute       TradeState = "DISPUTE"        // Manual intervention needed
	TradeDisputeClosed TradeState = "DISPUTE_CLOSED" // Closed without payout after review
	TradeCancelled     TradeState = "CANCELLED"      // Trade cancelled
)

// ──────────────────────────────────────────────
// TRADE EVENT DEFINITIONS
// Events are triggered by different actors:
// - User: accepts quote, cancels trade, raises dispute
// - System: starts conversion, initiates payout
// - Blockchain Watcher: detects deposit, confirms deposit
// - Admin: resolves disputes
// ──────────────────────────────────────────────

type TradeEvent string

const (
	EventQuoteCreated      TradeEvent = "QUOTE_CREATED"       // Pricing engine created a quote
	EventQuoteAccepted     TradeEvent = "QUOTE_ACCEPTED"      // User said "confirm"
	EventQuoteExpired      TradeEvent = "QUOTE_EXPIRED"       // 2-minute TTL passed
	EventDepositDetected   TradeEvent = "DEPOSIT_DETECTED"    // Blockchain tx seen (0 confirms)
	EventDepositConfirmed  TradeEvent = "DEPOSIT_CONFIRMED"   // Required confirmations met
	EventConversionStarted TradeEvent = "CONVERSION_STARTED"  // Exchange sell order placed
	EventConversionDone    TradeEvent = "CONVERSION_DONE"     // Exchange order filled
	EventPayoutInitiated   TradeEvent = "PAYOUT_INITIATED"    // Graph Finance payout started
	EventPayoutSuccess     TradeEvent = "PAYOUT_SUCCESS"      // NIP confirmation received
	EventPayoutFailed      TradeEvent = "PAYOUT_FAILED"       // NIP error after retries
	EventDisputeRaised     TradeEvent = "DISPUTE_RAISED"      // User or system flags issue
	EventDisputeResolved   TradeEvent = "DISPUTE_RESOLVED"    // Admin resolves in user's favor
	EventCancelled         TradeEvent = "CANCELLED"           // Trade cancelled (timeout or user)
)

// ──────────────────────────────────────────────
// TRANSITION TABLE
// ──────────────────────────────────────────────

// TradeTransition defines one valid state change for a trade.
type TradeTransition struct {
	From  TradeState
	Event TradeEvent
	To    TradeState
	Guard func(t *domain.Trade) error // optional validation before transition
}

// tradeTransitions is the complete list of valid trade state changes.
// This is the single source of truth for the trade lifecycle.
// Any transition not listed here is FORBIDDEN.
var tradeTransitions = []TradeTransition{

	// ────── Quote Phase ──────

	// User accepts the quote → trade is now waiting for crypto deposit
	{
		From:  TradeQuoteProvided,
		Event: EventQuoteAccepted,
		To:    TradePendingDeposit,
		Guard: func(t *domain.Trade) error {
			// GUARD: The quote must still be valid (within its 2-minute TTL).
			// ValidUntil comes from the associated Quote record.
			// If the user took too long to decide, the price is no longer guaranteed.
			if time.Now().After(t.ValidUntil) {
				return fmt.Errorf("quote has expired — please request a new quote")
			}
			return nil
		},
	},

	// Quote's 2-minute TTL passed without user accepting
	{
		From:  TradeQuoteProvided,
		Event: EventQuoteExpired,
		To:    TradeQuoteExpired,
		// No guard — expiry is determined by a scheduled job, not user action
	},

	// ────── Deposit Phase ──────

	// Blockchain watcher detected an incoming transaction (0 confirmations)
	{
		From:  TradePendingDeposit,
		Event: EventDepositDetected,
		To:    TradeDepositReceived,
		Guard: func(t *domain.Trade) error {
			// GUARD: The deposit must arrive within the 30-minute window.
			// After 30 minutes, the quoted exchange rate is no longer valid
			// and the trade should be cancelled instead.
			if time.Now().After(t.ExpiresAt) {
				return fmt.Errorf("deposit window expired — trade will be cancelled")
			}
			return nil
		},
	},

	// User or system cancels the trade while waiting for deposit
	{
		From:  TradePendingDeposit,
		Event: EventCancelled,
		To:    TradeCancelled,
		// No guard — cancellation is always allowed during the deposit wait
	},

	// Blockchain has enough confirmations (2 for BTC, 12 for USDC/ETH)
	{
		From:  TradeDepositReceived,
		Event: EventDepositConfirmed,
		To:    TradeDepositConfirmed,
		Guard: func(t *domain.Trade) error {
			// GUARD: Check minimum blockchain confirmations.
			//
			// Why 2 confirmations for BTC?
			// Each confirmation means another block was mined on top of the
			// block containing the user's transaction. More confirmations =
			// harder to reverse. 2 confirms for BTC ≈ 20 minutes, which
			// balances security vs. user experience.
			//
			// For USDC/ETH on Ethereum, 12 confirmations is standard
			// (≈ 3 minutes with 15-second blocks).
			if t.FromCurrency == "BTC" && t.Confirmations < 2 {
				return fmt.Errorf("insufficient confirmations: %d/2 required", t.Confirmations)
			}
			if (t.FromCurrency == "USDC" || t.FromCurrency == "ETH") && t.Confirmations < 12 {
				return fmt.Errorf("insufficient confirmations: %d/12 required", t.Confirmations)
			}
			return nil
		},
	},

	// ────── Conversion Phase ──────

	// System places a sell order on Binance (or Bybit fallback)
	{
		From:  TradeDepositConfirmed,
		Event: EventConversionStarted,
		To:    TradeConversionInProgress,
		// No guard — if deposit is confirmed, conversion can always start
	},

	// Exchange order has been completely filled
	{
		From:  TradeConversionInProgress,
		Event: EventConversionDone,
		To:    TradeConversionCompleted,
		// No guard — the exchange API confirms the fill
	},

	// Something went wrong during exchange conversion
	{
		From:  TradeConversionInProgress,
		Event: EventDisputeRaised,
		To:    TradeDispute,
		// No guard — disputes are always allowed when money is in limbo
	},

	// ────── Payout Phase ──────

	// System sends NGN to user's bank via Graph Finance NIP
	{
		From:  TradeConversionCompleted,
		Event: EventPayoutInitiated,
		To:    TradePayoutPending,
		// No guard — if conversion is done, payout can proceed
	},

	// Graph Finance confirms NIP transfer was successful
	{
		From:  TradePayoutPending,
		Event: EventPayoutSuccess,
		To:    TradePayoutCompleted,
		// No guard — the NIP confirmation is authoritative
	},

	// NIP transfer failed (wrong account, bank down, etc.)
	{
		From:  TradePayoutPending,
		Event: EventPayoutFailed,
		To:    TradePayoutFailed,
		// No guard — failure is reported by Graph Finance
	},

	// ────── Recovery Paths ──────

	// Retry payout after failure (automatic or admin-triggered)
	{
		From:  TradePayoutFailed,
		Event: EventPayoutInitiated,
		To:    TradePayoutPending, // Goes back to pending for another attempt
		// No guard — retries are allowed (the system has retry limits elsewhere)
	},

	// Payout keeps failing → escalate to dispute
	{
		From:  TradePayoutFailed,
		Event: EventDisputeRaised,
		To:    TradeDispute,
	},

	// Admin resolves dispute in user's favor → payout completed
	{
		From:  TradeDispute,
		Event: EventDisputeResolved,
		To:    TradePayoutCompleted,
	},
}

// ──────────────────────────────────────────────
// THE TRADE FSM ENGINE
// ──────────────────────────────────────────────

// TradeFSM is the Trade Finite State Machine.
type TradeFSM struct {
	transitions map[TradeState]map[TradeEvent]TradeTransition
}

// NewTradeFSM creates and initializes the Trade FSM.
func NewTradeFSM() *TradeFSM {
	fsm := &TradeFSM{
		transitions: make(map[TradeState]map[TradeEvent]TradeTransition),
	}
	for _, t := range tradeTransitions {
		if fsm.transitions[t.From] == nil {
			fsm.transitions[t.From] = make(map[TradeEvent]TradeTransition)
		}
		fsm.transitions[t.From][t.Event] = t
	}
	return fsm
}

// Transition attempts to move a trade to a new state.
//
// Returns nil on success, or an error describing why the transition failed.
// On success, t.Status is updated to the new state.
// The caller is responsible for:
//   1. Saving the updated trade to the database
//   2. Inserting a record into trade_status_history
func (f *TradeFSM) Transition(ctx context.Context, t *domain.Trade, event TradeEvent) error {
	currentState := TradeState(t.Status)

	// Step 1: Any transitions from this state?
	stateMap, ok := f.transitions[currentState]
	if !ok {
		return fmt.Errorf("no transitions defined from state %s", t.Status)
	}

	// Step 2: Is this event valid from this state?
	transition, ok := stateMap[event]
	if !ok {
		return fmt.Errorf(
			"event %s is not valid in state %s — check the state transition table",
			event, t.Status,
		)
	}

	// Step 3: Does the guard allow it?
	if transition.Guard != nil {
		if err := transition.Guard(t); err != nil {
			return fmt.Errorf("transition blocked by guard: %w", err)
		}
	}

	// Step 4: Apply the state change
	t.Status = string(transition.To)

	return nil
}

// CanTransition checks if a transition is possible WITHOUT applying it.
// Useful for showing the user what actions are available.
func (f *TradeFSM) CanTransition(t *domain.Trade, event TradeEvent) bool {
	currentState := TradeState(t.Status)
	stateMap, ok := f.transitions[currentState]
	if !ok {
		return false
	}
	transition, ok := stateMap[event]
	if !ok {
		return false
	}
	if transition.Guard != nil {
		return transition.Guard(t) == nil
	}
	return true
}

// ValidEvents returns all events that are currently valid from the trade's state.
// Useful for building UI elements that show available actions.
func (f *TradeFSM) ValidEvents(t *domain.Trade) []TradeEvent {
	currentState := TradeState(t.Status)
	stateMap, ok := f.transitions[currentState]
	if !ok {
		return nil
	}
	events := make([]TradeEvent, 0, len(stateMap))
	for event := range stateMap {
		events = append(events, event)
	}
	return events
}
