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
	"sync"
	"time"

	"convert-chain/go-engine/internal/exchange"
)

const (
	BaseURL                     = "https://api.binance.com"
	TestnetURL                  = "https://testnet.binance.vision"
	signedRequestRecvWindowMS   = int64(5000)
	serverTimeOffsetCacheWindow = 30 * time.Second
)

type Client struct {
	apiKey     string
	secretKey  string
	baseURL    string
	httpClient *http.Client

	mu                  sync.Mutex
	timeOffset          time.Duration
	timeOffsetExpiresAt time.Time
}

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

func (c *Client) Name() string {
	return "binance"
}

func (c *Client) sign(queryString string) string {
	mac := hmac.New(sha256.New, []byte(c.secretKey))
	mac.Write([]byte(queryString))
	return hex.EncodeToString(mac.Sum(nil))
}

func (c *Client) GetSpotPrice(ctx context.Context, symbol string) (*big.Float, error) {
	reqURL := fmt.Sprintf("%s/api/v3/ticker/price?symbol=%s", c.baseURL, symbol)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("binance HTTP error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("binance API error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Symbol string `json:"symbol"`
		Price  string `json:"price"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode binance response: %w", err)
	}

	price, ok := new(big.Float).SetString(result.Price)
	if !ok {
		return nil, fmt.Errorf("invalid price format from binance: %q", result.Price)
	}

	return price, nil
}

func (c *Client) PlaceMarketOrder(ctx context.Context, symbol, side, quantity string) (*exchange.OrderResult, error) {
	if c.apiKey == "" || c.secretKey == "" {
		return nil, fmt.Errorf("binance API keys not configured - set BINANCE_API_KEY and BINANCE_SECRET_KEY in .env")
	}

	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", side)
	params.Set("type", "MARKET")
	params.Set("quantity", quantity)

	body, err := c.doSignedRequest(ctx, http.MethodPost, "/api/v3/order", params, true)
	if err != nil {
		return nil, err
	}

	var result struct {
		Symbol             string `json:"symbol"`
		OrderID            int64  `json:"orderId"`
		Price              string `json:"price"`
		ExecutedQty        string `json:"executedQty"`
		CumulativeQuoteQty string `json:"cummulativeQuoteQty"`
		Status             string `json:"status"`
		Code               int    `json:"code,omitempty"`
		Msg                string `json:"msg,omitempty"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode order response: %w", err)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("binance error %d: %s", result.Code, result.Msg)
	}

	return &exchange.OrderResult{
		OrderID:     strconv.FormatInt(result.OrderID, 10),
		Symbol:      result.Symbol,
		Side:        side,
		Status:      result.Status,
		ExecutedQty: result.ExecutedQty,
		Price:       result.Price,
		QuoteQty:    result.CumulativeQuoteQty,
	}, nil
}

func (c *Client) GetBalance(ctx context.Context, asset string) (string, error) {
	if c.apiKey == "" || c.secretKey == "" {
		return "0", fmt.Errorf("binance API keys not configured")
	}

	body, err := c.doSignedRequest(ctx, http.MethodGet, "/api/v3/account", url.Values{}, true)
	if err != nil {
		return "0", err
	}

	var account struct {
		Code     int    `json:"code,omitempty"`
		Msg      string `json:"msg,omitempty"`
		Balances []struct {
			Asset string `json:"asset"`
			Free  string `json:"free"`
		} `json:"balances"`
	}
	if err := json.Unmarshal(body, &account); err != nil {
		return "0", fmt.Errorf("decode account response: %w", err)
	}
	if account.Code != 0 {
		return "0", fmt.Errorf("binance error %d: %s", account.Code, account.Msg)
	}

	for _, balance := range account.Balances {
		if balance.Asset == asset {
			return balance.Free, nil
		}
	}

	return "0", nil
}

type apiErrorResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func (c *Client) doSignedRequest(
	ctx context.Context,
	method string,
	path string,
	params url.Values,
	retryOnTimeDrift bool,
) ([]byte, error) {
	queryString, err := c.buildSignedQueryString(ctx, params, false)
	if err != nil {
		return nil, err
	}

	body, apiErr, statusCode, err := c.doRequest(ctx, method, path, queryString, true)
	if err != nil {
		return nil, fmt.Errorf("binance %s HTTP error: %w", path, err)
	}

	if apiErr != nil && apiErr.Code == -1021 && retryOnTimeDrift {
		queryString, err = c.buildSignedQueryString(ctx, params, true)
		if err != nil {
			return nil, err
		}

		body, apiErr, statusCode, err = c.doRequest(ctx, method, path, queryString, true)
		if err != nil {
			return nil, fmt.Errorf("binance %s HTTP error: %w", path, err)
		}
	}

	if apiErr != nil {
		return nil, fmt.Errorf("binance error %d: %s", apiErr.Code, apiErr.Msg)
	}
	if statusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("binance API error (HTTP %d): %s", statusCode, string(body))
	}

	return body, nil
}

func (c *Client) buildSignedQueryString(ctx context.Context, params url.Values, forceTimeSync bool) (string, error) {
	signedParams := cloneValues(params)

	timestamp, err := c.serverTimestampMillis(ctx, forceTimeSync)
	if err != nil {
		return "", fmt.Errorf("sync binance server time: %w", err)
	}

	signedParams.Set("timestamp", strconv.FormatInt(timestamp, 10))
	signedParams.Set("recvWindow", strconv.FormatInt(signedRequestRecvWindowMS, 10))

	queryString := signedParams.Encode()
	signedParams.Set("signature", c.sign(queryString))
	return signedParams.Encode(), nil
}

func (c *Client) serverTimestampMillis(ctx context.Context, force bool) (int64, error) {
	offset, err := c.refreshTimeOffset(ctx, force)
	if err != nil {
		return 0, err
	}
	return time.Now().Add(offset).UnixMilli(), nil
}

func (c *Client) refreshTimeOffset(ctx context.Context, force bool) (time.Duration, error) {
	c.mu.Lock()
	if !force && time.Now().Before(c.timeOffsetExpiresAt) {
		offset := c.timeOffset
		c.mu.Unlock()
		return offset, nil
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v3/time", nil)
	if err != nil {
		return 0, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("binance server time HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ServerTime int64 `json:"serverTime"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, err
	}

	offset := time.UnixMilli(result.ServerTime).Sub(time.Now())

	c.mu.Lock()
	c.timeOffset = offset
	c.timeOffsetExpiresAt = time.Now().Add(serverTimeOffsetCacheWindow)
	c.mu.Unlock()

	return offset, nil
}

func (c *Client) doRequest(
	ctx context.Context,
	method string,
	path string,
	queryString string,
	signed bool,
) ([]byte, *apiErrorResponse, int, error) {
	reqURL := c.baseURL + path
	if queryString != "" {
		reqURL += "?" + queryString
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	if signed {
		req.Header.Set("X-MBX-APIKEY", c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, resp.StatusCode, err
	}

	var apiErr apiErrorResponse
	if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Code != 0 {
		return body, &apiErr, resp.StatusCode, nil
	}

	return body, nil, resp.StatusCode, nil
}

func cloneValues(values url.Values) url.Values {
	cloned := url.Values{}
	for key, items := range values {
		for _, item := range items {
			cloned.Add(key, item)
		}
	}
	return cloned
}
