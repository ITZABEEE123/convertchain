// internal/kyc/sumsub/client.go
//
// Sumsub is a global identity verification platform that handles:
// - Facial liveness detection (anti-spoofing)
// - Document OCR and authenticity checking
// - Proof of address verification
// - Enhanced due diligence
//
// This is a stub implementation for now. The full integration will be
// built when the platform reaches Tier 2 KYC development.
//
// API Documentation: https://docs.sumsub.com/
package sumsub

import (
	"context"
	"net/http"
	"time"
)

// Client is the Sumsub API client.
type Client struct {
	appToken   string
	secretKey  string
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new Sumsub client.
func NewClient(appToken, secretKey string, sandbox bool) *Client {
	baseURL := "https://api.sumsub.com"
	if sandbox {
		baseURL = "https://test-api.sumsub.com"
	}
	return &Client{
		appToken:   appToken,
		secretKey:  secretKey,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// ──────────────────────────────────────────────
// LIVENESS CHECK
// ──────────────────────────────────────────────

// LivenessRequest contains the data needed for a liveness check.
type LivenessRequest struct {
	UserID     string // Our internal user ID
	SelfieData string // Base64-encoded selfie image
}

// LivenessResult contains the outcome of a liveness check.
type LivenessResult struct {
	IsLive     bool   // True if the selfie is a real, live person
	Confidence float64 // 0.0 to 1.0 confidence score
	RejectReason string
}

// CheckLiveness verifies that a selfie is a real, live person.
// TODO: Implement full Sumsub API integration
func (c *Client) CheckLiveness(ctx context.Context, req LivenessRequest) (*LivenessResult, error) {
	// STUB: In development, always return success.
	// Replace with real Sumsub API call in production.
	return &LivenessResult{
		IsLive:     true,
		Confidence: 0.95,
	}, nil
}

// ──────────────────────────────────────────────
// DOCUMENT VERIFICATION
// ──────────────────────────────────────────────

// DocVerifyRequest contains the data needed to verify a document.
type DocVerifyRequest struct {
	UserID     string
	DocType    string // "PROOF_OF_ADDRESS", "DRIVERS_LICENSE", etc.
	DocData    string // Base64-encoded document image
	MaxAgeDays int    // Document must be less than this many days old
}

// DocVerifyResult contains the outcome of document verification.
type DocVerifyResult struct {
	Verified     bool
	RejectReason string
	ExtractedData map[string]string // OCR-extracted fields (address, name, date)
}

// VerifyDocument checks the authenticity of an uploaded document.
// TODO: Implement full Sumsub API integration
func (c *Client) VerifyDocument(ctx context.Context, req DocVerifyRequest) (*DocVerifyResult, error) {
	// STUB: In development, always return success.
	return &DocVerifyResult{
		Verified:      true,
		ExtractedData: map[string]string{},
	}, nil
}