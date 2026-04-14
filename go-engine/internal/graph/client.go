// internal/graph/client.go
//
// Graph Finance API client for ConvertChain.
//
// Graph Finance provides:
// 1. Person management — register users for KYC/AML compliance
// 2. Deposit addresses — generate USDC deposit addresses for users
// 3. Conversions — convert USDC to NGN at market rate
// 4. NIP Payouts — send NGN directly to Nigerian bank accounts
// 5. Bank resolution — verify bank account names via NIP name enquiry
// 6. Exchange rates — get current USDC/NGN rate
//
// Environments:
//   Sandbox:    https://api.sandbox.usegraph.com/v1  (testing with fake money)
//   Production: https://api.usegraph.com/v1          (real money)
//
// Authentication: API key passed in the "x-api-key" header.
//
// Documentation: https://docs.usegraph.com/
package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"
)

// ──────────────────────────────────────────────
// CLIENT
// ──────────────────────────────────────────────

// Client is the Graph Finance API client.
type Client struct {
	// apiKey authenticates all requests via the x-api-key header.
	apiKey string

	// baseURL is either sandbox or production.
	baseURL string

	// httpClient is reused for connection pooling.
	httpClient *http.Client
}

// NewClient creates a new Graph Finance client.
//
// Parameters:
//   - apiKey: your Graph Finance API key (from their dashboard)
//   - sandbox: true for testing (fake money), false for production (real money)
func NewClient(apiKey string, sandbox bool) *Client {
	base := "https://api.usegraph.com/v1"
	if sandbox {
		base = "https://api.sandbox.usegraph.com/v1"
	}
	return &Client{
		apiKey:  apiKey,
		baseURL: base,
		httpClient: &http.Client{
			// 30-second timeout — Graph Finance operations (especially NIP payouts)
			// can take longer than exchange API calls because they involve
			// inter-bank communication.
			Timeout: 30 * time.Second,
		},
	}
}

// ──────────────────────────────────────────────
// HTTP HELPER
//
// All Graph Finance requests follow the same pattern:
//   1. Serialize body to JSON (if present)
//   2. Create HTTP request with context
//   3. Add x-api-key and Content-Type headers
//   4. Execute the request
//
// This helper eliminates code duplication across all endpoints.
// ──────────────────────────────────────────────

func (c *Client) do(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	// Serialize body to JSON (for POST/PUT requests)
	var bodyReader *bytes.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	} else {
		bodyReader = bytes.NewReader(nil)
	}

	// Create the HTTP request
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Add authentication and content type headers
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	// Execute
	return c.httpClient.Do(req)
}

// decodeError extracts the error message from a non-success response.
func decodeError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var errResp struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &errResp) == nil && errResp.Message != "" {
		return fmt.Errorf("graph API error (HTTP %d): %s", resp.StatusCode, errResp.Message)
	}
	return fmt.Errorf("graph API error (HTTP %d): %s", resp.StatusCode, string(body))
}

// ──────────────────────────────────────────────
// PERSON MANAGEMENT
//
// Before a user can receive payouts, they must be registered as a
// "Person" in Graph Finance's system. This is part of Graph's own
// KYC/AML compliance — they need to know who is receiving money.
//
// Flow: Our KYC approves the user → we create a Person in Graph →
//       Graph returns a person_id → we store it on the users table
// ──────────────────────────────────────────────

// CreatePersonRequest contains the data to register a user with Graph Finance.
type CreatePersonRequest struct {
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Email       string `json:"email"`
	PhoneNumber string `json:"phone_number"`
	DateOfBirth string `json:"date_of_birth"` // YYYY-MM-DD
	Country     string `json:"country"`        // "NG" for Nigeria
	IDType      string `json:"id_type"`        // "NIN" or "BVN"
	IDNumber    string `json:"id_number"`       // The NIN or BVN number
}

// Person represents a registered person in Graph Finance.
type Person struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	KYCLevel int    `json:"kyc_level"`
}

// CreatePerson registers a user with Graph Finance.
// Call this after KYC is approved — the person_id is needed for payouts.
func (c *Client) CreatePerson(ctx context.Context, req CreatePersonRequest) (*Person, error) {
	resp, err := c.do(ctx, http.MethodPost, "/people", req)
	if err != nil {
		return nil, fmt.Errorf("create person: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, decodeError(resp)
	}

	var person Person
	if err := json.NewDecoder(resp.Body).Decode(&person); err != nil {
		return nil, fmt.Errorf("decode person response: %w", err)
	}
	return &person, nil
}

// ──────────────────────────────────────────────
// DEPOSIT ADDRESSES
//
// Graph Finance can generate crypto deposit addresses for your users.
// When a user wants to sell USDC, you create a deposit address and
// tell the user to send their USDC there.
// ──────────────────────────────────────────────

type CreateDepositAddressRequest struct {
	PersonID string `json:"person_id"`
	Currency string `json:"currency"` // "USDC"
	Network  string `json:"network"`  // "ethereum", "polygon", "tron"
}

type DepositAddress struct {
	ID       string `json:"id"`
	Address  string `json:"address"`  // The blockchain address
	Currency string `json:"currency"`
	Network  string `json:"network"`
}

func (c *Client) CreateDepositAddress(ctx context.Context, req CreateDepositAddressRequest) (*DepositAddress, error) {
	resp, err := c.do(ctx, http.MethodPost, "/deposit-addresses", req)
	if err != nil {
		return nil, fmt.Errorf("create deposit address: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, decodeError(resp)
	}

	var addr DepositAddress
	if err := json.NewDecoder(resp.Body).Decode(&addr); err != nil {
		return nil, fmt.Errorf("decode deposit address response: %w", err)
	}
	return &addr, nil
}

// ──────────────────────────────────────────────
// CONVERSIONS
//
// After receiving USDC from a user's crypto sale, we convert it to NGN
// using Graph Finance's conversion service.
//
// Flow: USDC arrives → CreateConversion(USDC → NGN) → NGN balance ready
// ──────────────────────────────────────────────

type CreateConversionRequest struct {
	SourceCurrency      string `json:"source_currency"`      // "USDC"
	DestinationCurrency string `json:"destination_currency"` // "NGN"
	Amount              string `json:"amount"`               // String to avoid float issues
	PersonID            string `json:"person_id"`
}

type Conversion struct {
	ID                string `json:"id"`
	Status            string `json:"status"`
	SourceAmount      string `json:"source_amount"`
	DestinationAmount string `json:"destination_amount"`
	Rate              string `json:"rate"`
}

func (c *Client) CreateConversion(ctx context.Context, req CreateConversionRequest) (*Conversion, error) {
	resp, err := c.do(ctx, http.MethodPost, "/conversions", req)
	if err != nil {
		return nil, fmt.Errorf("create conversion: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, decodeError(resp)
	}

	var conv Conversion
	if err := json.NewDecoder(resp.Body).Decode(&conv); err != nil {
		return nil, fmt.Errorf("decode conversion response: %w", err)
	}
	return &conv, nil
}

// ──────────────────────────────────────────────
// NIP PAYOUTS
//
// NIP (Nigeria Inter-Bank Payment System) is the real-time bank transfer
// network in Nigeria. When we have NGN ready, we initiate a NIP payout
// to the user's bank account.
//
// Flow: NGN balance → CreatePayout → NIP transfer → Money in user's bank
//
// NIP payouts are usually instant (< 30 seconds) but can take up to
// 24 hours in rare cases (bank maintenance, system issues).
// ──────────────────────────────────────────────

type CreatePayoutRequest struct {
	PersonID      string `json:"person_id"`
	DestinationID string `json:"destination_id"` // Registered bank account ID
	Amount        string `json:"amount"`          // NGN amount as string
	Currency      string `json:"currency"`        // "NGN"
	Narration     string `json:"narration"`       // Transfer description shown on bank statement
}

type Payout struct {
	ID        string `json:"id"`
	Status    string `json:"status"`    // "pending", "completed", "failed"
	Amount    string `json:"amount"`
	CreatedAt string `json:"created_at"`
}

func (c *Client) CreatePayout(ctx context.Context, req CreatePayoutRequest) (*Payout, error) {
	resp, err := c.do(ctx, http.MethodPost, "/payouts", req)
	if err != nil {
		return nil, fmt.Errorf("create payout: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, decodeError(resp)
	}

	var payout Payout
	if err := json.NewDecoder(resp.Body).Decode(&payout); err != nil {
		return nil, fmt.Errorf("decode payout response: %w", err)
	}
	return &payout, nil
}

// ──────────────────────────────────────────────
// EXCHANGE RATES
// ──────────────────────────────────────────────

// GetRate fetches the current conversion rate between two currencies.
// Example: GetRate(ctx, "USDC", "NGN") returns how many NGN per 1 USDC.
func (c *Client) GetRate(ctx context.Context, from, to string) (*big.Float, error) {
	path := fmt.Sprintf("/rates?source_currency=%s&destination_currency=%s", from, to)
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("get rate: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, decodeError(resp)
	}

	var result struct {
		Rate string `json:"rate"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode rate response: %w", err)
	}

	rate, ok := new(big.Float).SetString(result.Rate)
	if !ok {
		return nil, fmt.Errorf("invalid rate format from Graph: %q", result.Rate)
	}
	return rate, nil
}

// ──────────────────────────────────────────────
// BANK RESOLUTION
//
// Before registering a user's bank account, we verify it exists and
// get the account holder's name. This uses NIP's "name enquiry" feature.
//
// Example:
//   ResolveBankAccount(ctx, "058", "0123456789")
//   → returns "ADE JOHNSON"
//
// If the returned name doesn't match the user's KYC name, we flag it.
// ──────────────────────────────────────────────

func (c *Client) ResolveBankAccount(ctx context.Context, bankCode, accountNumber string) (string, error) {
	body := map[string]string{
		"bank_code":      bankCode,
		"account_number": accountNumber,
	}
	resp, err := c.do(ctx, http.MethodPost, "/banks/resolve", body)
	if err != nil {
		return "", fmt.Errorf("resolve bank account: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", decodeError(resp)
	}

	var result struct {
		AccountName string `json:"account_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode resolve response: %w", err)
	}
	return result.AccountName, nil
}