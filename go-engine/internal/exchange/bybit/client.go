// internal/exchange/bybit/client.go
//
// Bybit V5 Unified API client — fallback exchange for ConvertChain.
//
// Bybit uses a different authentication scheme than Binance:
// - Binance: sign the query string parameters
// - Bybit: sign timestamp + api_key + recv_window + request_body
//
// Bybit V5 API Documentation: https://bybit-exchange.github.io/docs/v5/intro
package bybit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"convert-chain/go-engine/internal/exchange"
)

const (
	BaseURL    = "https://api.bybit.com"
	TestnetURL = "https://api-testnet.bybit.com"
)

// Client is the Bybit V5 API client.
type Client struct {
	apiKey    string
	secretKey string
	baseURL   string
	httpClient *http.Client
}

// NewClient creates a new Bybit client.
func NewClient(apiKey, secretKey string, useTestnet bool) *Client {
	base := BaseURL
	if useTestnet {
		base = TestnetURL
	}
	return &Client{
		apiKey:    apiKey,
		secretKey: secretKey,
		baseURL:   base,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Name returns the exchange name.
func (c *Client) Name() string {
	return "bybit"
}

// ──────────────────────────────────────────────
// BYBIT SIGNING
//
// Bybit V5 uses a different signing format than Binance:
//   payload = timestamp + apiKey + recvWindow + requestBody
//   signature = HMAC-SHA256(payload, secretKey)
//
// The signature and other auth data go in HTTP headers, not URL params.
// ──────────────────────────────────────────────

func (c *Client) sign(payload string) string {
	mac := hmac.New(sha256.New, []byte(c.secretKey))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// addAuthHeaders adds authentication headers to a Bybit API request.
func (c *Client) addAuthHeaders(req *http.Request, body string) {
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recvWindow := "5000" // 5-second window for request validity

	// Bybit signs: timestamp + apiKey + recvWindow + body
	payload := timestamp + c.apiKey + recvWindow + body
	signature := c.sign(payload)

	req.Header.Set("X-BAPI-API-KEY", c.apiKey)
	req.Header.Set("X-BAPI-TIMESTAMP", timestamp)
	req.Header.Set("X-BAPI-SIGN", signature)
	req.Header.Set("X-BAPI-RECV-WINDOW", recvWindow)
	req.Header.Set("Content-Type", "application/json")
}

// ──────────────────────────────────────────────
// PUBLIC ENDPOINTS
// ──────────────────────────────────────────────

// GetSpotPrice retrieves the current spot price from Bybit.
//
// Bybit API: GET /v5/market/tickers?category=spot&symbol=BTCUSDT
// Documentation: https://bybit-exchange.github.io/docs/v5/market/tickers
func (c *Client) GetSpotPrice(ctx context.Context, symbol string) (*big.Float, error) {
	reqURL := fmt.Sprintf("%s/v5/market/tickers?category=spot&symbol=%s", c.baseURL, symbol)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bybit HTTP error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("bybit API error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	// Bybit V5 response format is wrapped in a standard envelope:
	// { "retCode": 0, "result": { "list": [{"lastPrice": "67123.45", ...}] } }
	var result struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []struct {
				Symbol    string `json:"symbol"`
				LastPrice string `json:"lastPrice"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode bybit response: %w", err)
	}

	// Check for Bybit-level errors
	if result.RetCode != 0 {
		return nil, fmt.Errorf("bybit error %d: %s", result.RetCode, result.RetMsg)
	}

	if len(result.Result.List) == 0 {
		return nil, fmt.Errorf("no price data from bybit for %s", symbol)
	}

	price, ok := new(big.Float).SetString(result.Result.List[0].LastPrice)
	if !ok {
		return nil, fmt.Errorf("invalid price format from bybit: %q", result.Result.List[0].LastPrice)
	}

	return price, nil
}

// ──────────────────────────────────────────────
// PRIVATE ENDPOINTS
// ──────────────────────────────────────────────

// PlaceMarketOrder places a market order on Bybit.
//
// Bybit API: POST /v5/order/create
// Documentation: https://bybit-exchange.github.io/docs/v5/order/create-order
func (c *Client) PlaceMarketOrder(ctx context.Context, symbol, side, quantity string) (*exchange.OrderResult, error) {
	if c.apiKey == "" || c.secretKey == "" {
		return nil, fmt.Errorf("bybit API keys not configured — set BYBIT_API_KEY and BYBIT_SECRET_KEY in .env")
	}

	// Build the JSON request body
	bodyMap := map[string]string{
		"category":  "spot",
		"symbol":    symbol,
		"side":      side,       // "Sell"
		"orderType": "Market",
		"qty":       quantity,
	}
	bodyBytes, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, fmt.Errorf("marshal order body: %w", err)
	}
	bodyStr := string(bodyBytes)

	// Create the request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v5/order/create", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create order request: %w", err)
	}

	// Add authentication headers
	c.addAuthHeaders(req, bodyStr)

	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bybit order HTTP error: %w", err)
	}
	defer resp.Body.Close()

	// Parse the response
	var bybitResult struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			OrderID     string `json:"orderId"`
			OrderLinkID string `json:"orderLinkId"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&bybitResult); err != nil {
		return nil, fmt.Errorf("decode order response: %w", err)
	}

	if bybitResult.RetCode != 0 {
		return nil, fmt.Errorf("bybit error %d: %s", bybitResult.RetCode, bybitResult.RetMsg)
	}

	return &exchange.OrderResult{
		OrderID: bybitResult.Result.OrderID,
		Symbol:  symbol,
		Side:    side,
		Status:  "SUBMITTED", // Bybit doesn't return fill status immediately
	}, nil
}

// GetBalance fetches the available balance for a specific asset on Bybit.
func (c *Client) GetBalance(ctx context.Context, asset string) (string, error) {
	if c.apiKey == "" || c.secretKey == "" {
		return "0", fmt.Errorf("bybit API keys not configured")
	}

	reqURL := c.baseURL + "/v5/account/wallet-balance?accountType=UNIFIED&coin=" + asset

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "0", err
	}

	// GET requests: sign with empty body
	c.addAuthHeaders(req, "")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "0", err
	}
	defer resp.Body.Close()

	var result struct {
		Result struct {
			List []struct {
				Coin []struct {
					Coin            string `json:"coin"`
					WalletBalance   string `json:"walletBalance"`
					AvailableToWithdraw string `json:"availableToWithdraw"`
				} `json:"coin"`
			} `json:"list"`
		} `json:"result"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result.Result.List) > 0 {
		for _, coin := range result.Result.List[0].Coin {
			if coin.Coin == asset {
				return coin.AvailableToWithdraw, nil
			}
		}
	}

	return "0", nil
}