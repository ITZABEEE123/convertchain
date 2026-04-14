// internal/statemachine/user_fsm_test.go
package statemachine

import (
	"context"
	"testing"
	"time"

	"convert-chain/go-engine/internal/domain"
)

// TestUserFSM_HappyPath tests the complete successful registration flow:
// UNREGISTERED → KYC_IN_PROGRESS → KYC_PENDING → KYC_APPROVED
func TestUserFSM_HappyPath(t *testing.T) {
	fsm := NewUserFSM()
	ctx := context.Background()

	// Create a fresh user (starts as UNREGISTERED)
	now := time.Now()
	user := &domain.User{
		Status:         string(StateUnregistered),
		ConsentGivenAt: &now,          // Consent already recorded
		PhoneNumber:    "2348012345678", // Phone number collected
	}

	// Step 1: Give consent → should move to KYC_IN_PROGRESS
	err := fsm.Transition(ctx, user, EventConsentGiven)
	if err != nil {
		t.Fatalf("Expected consent transition to succeed, got: %v", err)
	}
	if user.Status != string(StateKYCInProgress) {
		t.Fatalf("Expected status KYC_IN_PROGRESS, got: %s", user.Status)
	}

	// Step 2: Submit KYC → should move to KYC_PENDING
	err = fsm.Transition(ctx, user, EventKYCSubmitted)
	if err != nil {
		t.Fatalf("Expected KYC submission to succeed, got: %v", err)
	}
	if user.Status != string(StateKYCPending) {
		t.Fatalf("Expected status KYC_PENDING, got: %s", user.Status)
	}

	// Step 3: KYC approved → should move to KYC_APPROVED
	err = fsm.Transition(ctx, user, EventKYCApproved)
	if err != nil {
		t.Fatalf("Expected KYC approval to succeed, got: %v", err)
	}
	if user.Status != string(StateKYCApproved) {
		t.Fatalf("Expected status KYC_APPROVED, got: %s", user.Status)
	}
}

// TestUserFSM_ConsentGuard tests that the consent guard blocks
// the transition when ConsentGivenAt is not set.
func TestUserFSM_ConsentGuard(t *testing.T) {
	fsm := NewUserFSM()
	ctx := context.Background()

	// User WITHOUT consent timestamp
	user := &domain.User{
		Status:         string(StateUnregistered),
		ConsentGivenAt: nil, // No consent!
	}

	err := fsm.Transition(ctx, user, EventConsentGiven)
	if err == nil {
		t.Fatal("Expected transition to FAIL because consent timestamp is missing")
	}

	// User should still be in UNREGISTERED (state not changed)
	if user.Status != string(StateUnregistered) {
		t.Fatalf("Expected status to remain UNREGISTERED, got: %s", user.Status)
	}
}

// TestUserFSM_PhoneGuard tests that submitting KYC fails without a phone number.
func TestUserFSM_PhoneGuard(t *testing.T) {
	fsm := NewUserFSM()
	ctx := context.Background()

	user := &domain.User{
		Status:      string(StateKYCInProgress),
		PhoneNumber: "", // No phone number!
	}

	err := fsm.Transition(ctx, user, EventKYCSubmitted)
	if err == nil {
		t.Fatal("Expected transition to FAIL because phone number is missing")
	}
}

// TestUserFSM_InvalidTransition tests that impossible transitions are rejected.
func TestUserFSM_InvalidTransition(t *testing.T) {
	fsm := NewUserFSM()
	ctx := context.Background()

	user := &domain.User{
		Status: string(StateUnregistered),
	}

	// Try to submit KYC while UNREGISTERED (should fail — must give consent first)
	err := fsm.Transition(ctx, user, EventKYCSubmitted)
	if err == nil {
		t.Fatal("Expected transition to FAIL — can't submit KYC from UNREGISTERED")
	}
}

// TestUserFSM_RetryAfterRejection tests the 24-hour cooldown after KYC rejection.
func TestUserFSM_RetryAfterRejection(t *testing.T) {
	fsm := NewUserFSM()
	ctx := context.Background()

	// User was rejected 1 hour ago (should NOT be able to retry yet)
	oneHourAgo := time.Now().Add(-1 * time.Hour)
	user := &domain.User{
		Status:         string(StateKYCRejected),
		LastRejectedAt: &oneHourAgo,
	}

	err := fsm.Transition(ctx, user, EventKYCRetry)
	if err == nil {
		t.Fatal("Expected retry to FAIL — 24-hour cooldown not yet passed")
	}

	// User was rejected 25 hours ago (should be able to retry)
	twentyFiveHoursAgo := time.Now().Add(-25 * time.Hour)
	user.LastRejectedAt = &twentyFiveHoursAgo

	err = fsm.Transition(ctx, user, EventKYCRetry)
	if err != nil {
		t.Fatalf("Expected retry to succeed after 25 hours, got: %v", err)
	}
	if user.Status != string(StateKYCInProgress) {
		t.Fatalf("Expected status KYC_IN_PROGRESS after retry, got: %s", user.Status)
	}
}