// internal/kyc/smileid/client.go
//
// SmileID is a Nigerian identity verification provider that connects
// to NIMC (National Identity Management Commission) databases.
// Their API lets you verify BVN numbers, NIN numbers, and perform
// facial recognition against government photo records.
//
// API Documentation: https://docs.usesmileid.com/
//
// This client handles:
// 1. HMAC signature generation (required for all SmileID API calls)
// 2. BVN lookup (verifies Bank Verification Number)
// 3. NIN lookup (verifies National Identification Number)
package smileid

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// ──────────────────────────────────────────────
// CONFIGURATION
// ──────────────────────────────────────────────

const (
	// BaseURLProduction is the live SmileID API endpoint.
	// Only use this with real user data and real API keys.
	BaseURLProduction = "https://api.smileidentity.com/v1"

	// BaseURLSandbox is the test environment.
	// Use this during development — it returns fake but realistic data
	// without hitting real NIMC databases (and without incurring charges).
	BaseURLSandbox = "https://testapi.smileidentity.com/v1"
)

// ──────────────────────────────────────────────
// CLIENT
// ──────────────────────────────────────────────

// Client is the SmileID API client. It handles authentication,
// request signing, and API communication.
type Client struct {
	// partnerID is your SmileID account identifier.
	// Obtained from the SmileID dashboard when you register.
	partnerID string

	// apiKey is your secret API key. Used to generate HMAC signatures.
	// NEVER log this value. Store it in HashiCorp Vault in production.
	apiKey string

	// baseURL is either production or sandbox depending on environment.
	baseURL string

	// httpClient is a reusable HTTP client with a 30-second timeout.
	// Reusing the client is important because it reuses TCP connections
	// (connection pooling), which is much faster than creating a new
	// connection for every API call.
	httpClient *http.Client
}

// NewClient creates a new SmileID API client.
//
// Parameters:
//   - partnerID: your SmileID partner ID (from dashboard)
//   - apiKey: your SmileID API secret key (from Vault in production)
//   - sandbox: true for development/testing, false for production
func NewClient(partnerID, apiKey string, sandbox bool) *Client {
	baseURL := BaseURLProduction
	if sandbox {
		baseURL = BaseURLSandbox
	}
	return &Client{
		partnerID: partnerID,
		apiKey:    apiKey,
		baseURL:   baseURL,
		httpClient: &http.Client{
			// 30-second timeout prevents hanging if SmileID is slow.
			// Without a timeout, a slow API could block your goroutine forever.
			Timeout: 30 * time.Second,
		},
	}
}

// ──────────────────────────────────────────────
// HMAC SIGNATURE GENERATION
//
// SmileID requires every API request to include an HMAC signature.
// This proves that the request came from someone who knows the API key,
// preventing unauthorized API usage.
//
// How HMAC works:
// 1. Combine timestamp + partner_id + "sid_request" into a message
// 2. Hash the message using SHA-256 with your API key as the secret
// 3. Base64-encode the hash
// 4. Include the result in the API request as "signature"
//
// SmileID receives the request, performs the same calculation with their
// copy of your API key, and checks if the signatures match.
// If they do → request is authentic. If not → request is rejected.
// ──────────────────────────────────────────────

func (c *Client) generateSignature(timestamp string) string {
	// The message format is defined by SmileID's API spec
	message := timestamp + c.partnerID + "sid_request"

	// Create a new HMAC hasher using SHA-256 and the API key
	mac := hmac.New(sha256.New, []byte(c.apiKey))

	// Write the message to the hasher
	mac.Write([]byte(message))

	// Get the hash result and encode it as Base64
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// ──────────────────────────────────────────────
// BVN LOOKUP
// ──────────────────────────────────────────────

// BVNLookupRequest contains the data needed to verify a BVN.
type BVNLookupRequest struct {
	BVN         string // 11-digit Bank Verification Number
	FirstName   string // For name matching
	LastName    string // For name matching
	DateOfBirth string // YYYY-MM-DD format, for DOB matching
}

// BVNLookupResult contains the data returned from a BVN verification.
type BVNLookupResult struct {
	FullName    string // Name on the BVN record
	DateOfBirth string // DOB on the BVN record
	PhoneNumber string // Phone number on the BVN record
	NameMatch   bool   // Whether the provided name matches the BVN name
	DOBMatch    bool   // Whether the provided DOB matches the BVN DOB
}

// LookupBVN verifies a Bank Verification Number against CBN records.
//
// The BVN system was created by the Central Bank of Nigeria in 2014 to
// give every bank customer a unique 11-digit identifier. It's linked to
// the person's biometrics (fingerprint, photo) and personal details.
func (c *Client) LookupBVN(ctx context.Context, req BVNLookupRequest) (*BVNLookupResult, error) {
	// Generate a timestamp for the signature (milliseconds since epoch)
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)

	// Build the API request payload
	// SmileID's ID Verification endpoint accepts a JSON body with
	// the ID type, number, and expected data for matching.
	payload := map[string]interface{}{
		"partner_id": c.partnerID,
		"timestamp":  timestamp,
		"signature":  c.generateSignature(timestamp),
		"id_type":    "BVN_MFA",   // Multi-Factor Authentication BVN lookup
		"id_number":  req.BVN,
		"country":    "NG",        // Nigeria
		"first_name": req.FirstName,
		"last_name":  req.LastName,
		"dob":        req.DateOfBirth,
	}

	// Serialize to JSON
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create the HTTP request with context (for cancellation/timeout)
	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/id_verification",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check HTTP status code
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("SmileID API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	// Parse the response JSON
	var result struct {
		FullName    string `json:"FullName"`
		DOB         string `json:"DOB"`
		PhoneNumber string `json:"PhoneNumber"`
		Actions     struct {
			VerifyID  string `json:"Verify_ID_Number"`
			NameCheck string `json:"Names_Check"`
		} `json:"Actions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode SmileID response: %w", err)
	}

	return &BVNLookupResult{
		FullName:    result.FullName,
		DateOfBirth: result.DOB,
		PhoneNumber: result.PhoneNumber,
		NameMatch:   result.Actions.NameCheck == "Exact",
		DOBMatch:    result.DOB == req.DateOfBirth,
	}, nil
}

// ──────────────────────────────────────────────
// NIN LOOKUP
// ──────────────────────────────────────────────

// NINLookupRequest contains the data needed to verify a NIN.
type NINLookupRequest struct {
	NIN string // 11-digit National Identification Number
}

// NINLookupResult contains the data returned from a NIN verification.
type NINLookupResult struct {
	Status      string // "VALID" or "NOT_FOUND"
	FullName    string
	DateOfBirth string
}

// LookupNIN verifies a National Identification Number against NIMC records.
func (c *Client) LookupNIN(ctx context.Context, req NINLookupRequest) (*NINLookupResult, error) {
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)

	payload := map[string]interface{}{
		"partner_id": c.partnerID,
		"timestamp":  timestamp,
		"signature":  c.generateSignature(timestamp),
		"id_type":    "NIN",
		"id_number":  req.NIN,
		"country":    "NG",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/id_verification",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("SmileID API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		FullName string `json:"FullName"`
		DOB      string `json:"DOB"`
		Actions  struct {
			VerifyID string `json:"Verify_ID_Number"`
		} `json:"Actions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode SmileID response: %w", err)
	}

	status := "NOT_FOUND"
	if result.Actions.VerifyID == "Verified" {
		status = "VALID"
	}

	return &NINLookupResult{
		Status:      status,
		FullName:    result.FullName,
		DateOfBirth: result.DOB,
	}, nil
}