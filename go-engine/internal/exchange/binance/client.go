// internal/exchange/binance/client.go
//
// Binance Spot API v3 client for ConvertChain.
//
// This client handles two types of API calls:
//
// 1. PUBLIC endpoints (no API key needed):
//    - GET /api/v3/ticker/price — current price for a trading pair
//    These are free and can be called without authentication.
//    You CAN test price queries right now without any Binance account.
//
// 2. PRIVATE endpoints (API key + signature required):
//    - POST /api/v3/order — place a trade order
//    - GET /api/v3/account — check account balances
//    These require a Binance account with API keys and HMAC-SHA256 signing.
//    You'll set these up when you're ready for live trading.
//
// API Documentation: https://binance-docs.github.io/apidocs/spot/en/
package binance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"convert-chain/go-engine/internal/exchange"
)

// ──────────────────────────────────────────────
// CONFIGURATION
// ──────────────────────────────────────────────

const (
	// BaseURL is the Binance Spot API base URL.
	// For the testnet (paper trading), use: "https://testnet.binance.vision"
	BaseURL = "https://api.binance.com"

	// TestnetURL is for testing without real money.
	// Binance provides a testnet that mimics the real API but uses fake funds.
	TestnetURL = "https://testnet.binance.vision"
)

// ──────────────────────────────────────────────
// CLIENT
// ──────────────────────────────────────────────

// Client is the Binance Spot API v3 client.
type Client struct {
	// apiKey identifies your Binance account.
	// Sent as the "X-MBX-APIKEY" header on every private request.
	// Can be empty for public-only endpoints (price queries).
	apiKey string

	// secretKey is used to generate HMAC-SHA256 signatures.
	// NEVER log this. NEVER commit this. Store in Vault in production.
	secretKey string

	// baseURL can be BaseURL (production) or TestnetURL (testing)
	baseURL string

	// httpClient is reused across all requests (connection pooling).
	httpClient *http.Client
}

// NewClient creates a new Binance API client.
//
// Parameters:
//   - apiKey: your Binance API key (empty string for public-only access)
//   - secretKey: your Binance API secret (empty string for public-only access)
//   - useTestnet: if true, uses the Binance testnet (fake money for testing)
func NewClient(apiKey, secretKey string, useTestnet bool) *Client {
	base := BaseURL
	if useTestnet {
		base = TestnetURL
	}
	return &Client{
		apiKey:    apiKey,
		secretKey: secretKey,
		baseURL:   base,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Name returns the exchange name (implements ExchangeClient interface).
func (c *Client) Name() string {
	return "binance"
}

// ──────────────────────────────────────────────
// HMAC-SHA256 SIGNING
//
// Private Binance endpoints require that you "sign" your request.
// The process:
//   1. Take all your request parameters as a query string
//      Example: "symbol=BTCUSDT&side=SELL&type=MARKET&quantity=0.25&timestamp=1234567890"
//   2. Create an HMAC-SHA256 hash using your secret key
//   3. Append the hash as &signature=... to the query string
//
// This proves to Binance that you (the API key holder) authorized
// this exact request. An attacker who intercepts the request cannot
// modify the parameters without invalidating the signature.
// ──────────────────────────────────────────────

func (c *Client) sign(queryString string) string {
	mac := hmac.New(sha256.New, []byte(c.secretKey))
	mac.Write([]byte(queryString))
	return hex.EncodeToString(mac.Sum(nil))
}

// ──────────────────────────────────────────────
// PUBLIC ENDPOINTS (No API key needed)
// ──────────────────────────────────────────────

// GetSpotPrice retrieves the current best price for a trading pair.
// This is a PUBLIC endpoint — works without API keys.
//
// Example: GetSpotPrice(ctx, "BTCUSDT") returns the current BTC/USDT price.
//
// Binance API endpoint: GET /api/v3/ticker/price
// Documentation: https://binance-docs.github.io/apidocs/spot/en/#symbol-price-ticker
func (c *Client) GetSpotPrice(ctx context.Context, symbol string) (*big.Float, error) {
	// Build the URL
	reqURL := fmt.Sprintf("%s/api/v3/ticker/price?symbol=%s", c.baseURL, symbol)

	// Create request with context (for cancellation/timeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("binance HTTP error: %w", err)
	}
	defer resp.Body.Close()

	// Check for HTTP errors
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("binance API error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	// Parse the JSON response
	// Binance returns: {"symbol":"BTCUSDT","price":"67123.45000000"}
	var result struct {
		Symbol string `json:"symbol"`
		Price  string `json:"price"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode binance response: %w", err)
	}

	// Convert the price string to big.Float
	// This is critical — we parse the string directly into big.Float,
	// never going through float64 (which would lose precision).
	price, ok := new(big.Float).SetString(result.Price)
	if !ok {
		return nil, fmt.Errorf("invalid price format from binance: %q", result.Price)
	}

	return price, nil
}

// ──────────────────────────────────────────────
// PRIVATE ENDPOINTS (API key + signature required)
// ──────────────────────────────────────────────

// PlaceMarketOrder places a market sell order.
//
// A market order means "sell immediately at the best available price."
// Unlike a limit order (sell only if price reaches X), a market order
// executes instantly but the exact price depends on the order book.
//
// For our platform, we always use market orders because:
// 1. Speed matters — the user is waiting for their NGN
// 2. The quote already locked in a price (with a small buffer)
// 3. Slippage on major pairs (BTCUSDT) is negligible for our volumes
//
// Binance API endpoint: POST /api/v3/order
// Documentation: https://binance-docs.github.io/apidocs/spot/en/#new-order-trade
func (c *Client) PlaceMarketOrder(ctx context.Context, symbol, side, quantity string) (*exchange.OrderResult, error) {
	// Check that API keys are configured
	if c.apiKey == "" || c.secretKey == "" {
		return nil, fmt.Errorf("binance API keys not configured — set BINANCE_API_KEY and BINANCE_SECRET_KEY in .env")
	}

	// Build the signed request parameters
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	params := url.Values{}
	params.Set("symbol", symbol)       // e.g., "BTCUSDT"
	params.Set("side", side)           // "SELL"
	params.Set("type", "MARKET")       // Market order (execute immediately)
	params.Set("quantity", quantity)    // e.g., "0.25000000"
	params.Set("timestamp", timestamp) // Current time in milliseconds

	// Sign the parameters
	queryString := params.Encode()
	signature := c.sign(queryString)
	params.Set("signature", signature)

	// Create the POST request
	reqURL := c.baseURL + "/api/v3/order?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create order request: %w", err)
	}

	// Add the API key header (required for all private endpoints)
	req.Header.Set("X-MBX-APIKEY", c.apiKey)

	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("binance order HTTP error: %w", err)
	}
	defer resp.Body.Close()

	// Parse the response
	var binanceResult struct {
		Symbol             string `json:"symbol"`
		OrderID            int64  `json:"orderId"`
		ClientOrderID      string `json:"clientOrderId"`
		TransactTime       int64  `json:"transactTime"`
		Price              string `json:"price"`
		OrigQty            string `json:"origQty"`
		ExecutedQty        string `json:"executedQty"`
		CumulativeQuoteQty string `json:"cummulativeQuoteQty"` // Note: Binance typo is intentional
		Status             string `json:"status"`
		Code               int    `json:"code,omitempty"` // Non-zero means error
		Msg                string `json:"msg,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&binanceResult); err != nil {
		return nil, fmt.Errorf("decode order response: %w", err)
	}

	// Check for Binance-level errors
	// Binance returns negative error codes: -1013, -2010, etc.
	if binanceResult.Code != 0 {
		return nil, fmt.Errorf("binance error %d: %s", binanceResult.Code, binanceResult.Msg)
	}

	// Convert to our standardized OrderResult
	return &exchange.OrderResult{
		OrderID:     strconv.FormatInt(binanceResult.OrderID, 10),
		Symbol:      binanceResult.Symbol,
		Side:        side,
		Status:      binanceResult.Status,
		ExecutedQty: binanceResult.ExecutedQty,
		Price:       binanceResult.Price,
		QuoteQty:    binanceResult.CumulativeQuoteQty,
	}, nil
}

// GetBalance fetches the available balance for a specific asset.
//
// Binance API endpoint: GET /api/v3/account
// This is a private endpoint that returns all balances.
// We filter for the specific asset the caller wants.
func (c *Client) GetBalance(ctx context.Context, asset string) (string, error) {
	if c.apiKey == "" || c.secretKey == "" {
		return "0", fmt.Errorf("binance API keys not configured")
	}

	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	params := url.Values{}
	params.Set("timestamp", timestamp)
	queryString := params.Encode()
	params.Set("signature", c.sign(queryString))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/v3/account?"+params.Encode(), nil)
	if err != nil {
		return "0", err
	}
	req.Header.Set("X-MBX-APIKEY", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "0", fmt.Errorf("binance account HTTP error: %w", err)
	}
	defer resp.Body.Close()

	var account struct {
		Balances []struct {
			Asset string `json:"asset"`
			Free  string `json:"free"`
		} `json:"balances"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&account); err != nil {
		return "0", fmt.Errorf("decode account response: %w", err)
	}

	// Find the requested asset in the balance list
	for _, b := range account.Balances {
		if b.Asset == asset {
			return b.Free, nil
		}
	}

	return "0", nil // Asset not found — balance is zero
}