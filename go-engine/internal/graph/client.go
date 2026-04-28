package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	apiKey     string
	baseURL    string
	sandbox    bool
	httpClient *http.Client
}

func NewClient(apiKey string, sandbox bool) *Client {
	return &Client{
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: "https://api.useoval.com",
		sandbox: sandbox,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) IsSandbox() bool {
	return c.sandbox
}

type envelope struct {
	Status  any             `json:"status"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message"`
	Error   *apiError       `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}

type ProviderError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "provider error"
	}
	if e.Code != "" {
		return fmt.Sprintf("oval api error (HTTP %d): %s: %s", e.StatusCode, e.Code, message)
	}
	return fmt.Sprintf("oval api error (HTTP %d): %s", e.StatusCode, message)
}

type WalletAccount struct {
	ID               string
	Currency         string
	AccountName      string
	AccountNumber    string
	BankName         string
	BankCode         string
	Balance          int64
	AvailableBalance int64
}

type FundingBankAccount struct {
	ID                      string
	Currency                string
	AccountName             string
	AccountNumber           string
	BankName                string
	BankCode                string
	SettlementCurrency      string
	SettlementWalletAccount string
}

type Bank struct {
	ID              string
	Code            string
	Name            string
	Slug            string
	NIPCode         string
	ShortCode       string
	Country         string
	Currency        string
	ResolveBankCode string
}

type ResolvedBankAccount struct {
	BankID        string
	BankCode      string
	BankName      string
	AccountNumber string
	AccountName   string
}

type CreatePayoutDestinationRequest struct {
	AccountID       string
	SourceType      string
	Label           string
	Type            string
	DestinationType string
	AccountType     string
	BankCode        string
	BankID          string
	AccountNumber   string
	BeneficiaryName string
}

type PayoutDestination struct {
	ID            string
	AccountName   string
	AccountNumber string
	BankName      string
	BankCode      string
}

type CreatePayoutRequest struct {
	AccountID       string
	SourceType      string
	DestinationID   string
	DestinationType string
	Currency        string
	Amount          int64
	Reference       string
	Remarks         string
}

type Payout struct {
	ID        string
	Status    string
	Amount    int64
	Currency  string
	CreatedAt string
}

type MockDepositRequest struct {
	AccountID   string
	SourceType  string
	Currency    string
	Amount      int64
	Reference   string
	Description string
	SenderName  string
}

type MockDepositResult struct {
	ID     string
	Status string
}

func (c *Client) request(ctx context.Context, method, path string, query url.Values, body any) (json.RawMessage, error) {
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		requestBody = bytes.NewReader(encoded)
	}

	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, requestBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeError(resp.StatusCode, responseBody)
	}

	var wrapped envelope
	if err := json.Unmarshal(responseBody, &wrapped); err == nil && len(wrapped.Data) > 0 && string(wrapped.Data) != "null" {
		return wrapped.Data, nil
	}

	return responseBody, nil
}

func decodeError(statusCode int, payload []byte) error {
	providerErr := &ProviderError{
		StatusCode: statusCode,
		Code:       fmt.Sprintf("GRAPH_HTTP_%d", statusCode),
		Message:    strings.TrimSpace(string(payload)),
	}

	var wrapped envelope
	if err := json.Unmarshal(payload, &wrapped); err == nil {
		if wrapped.Error != nil {
			if strings.TrimSpace(wrapped.Error.Code) != "" {
				providerErr.Code = strings.TrimSpace(wrapped.Error.Code)
			}
			if wrapped.Error.Message != "" {
				providerErr.Message = wrapped.Error.Message
			}
			details := stringifyAny(wrapped.Error.Details)
			if details != "" && providerErr.Message == "" {
				providerErr.Message = details
			}
		}
		if wrapped.Message != "" {
			providerErr.Message = wrapped.Message
		}
	}

	if providerErr.Message == "" {
		providerErr.Message = http.StatusText(statusCode)
	}
	return providerErr
}

func (c *Client) ListWalletAccounts(ctx context.Context) ([]WalletAccount, error) {
	raw, err := c.request(ctx, http.MethodGet, "/wallet_account", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("list wallet accounts: %w", err)
	}
	return parseWalletAccounts(raw)
}

func (c *Client) GetWalletAccountByCurrency(ctx context.Context, currency string) (*WalletAccount, error) {
	accounts, err := c.ListWalletAccounts(ctx)
	if err != nil {
		return nil, err
	}

	target := normalizeWalletCurrency(currency)
	for _, account := range accounts {
		if strings.EqualFold(account.Currency, target) {
			copy := account
			return &copy, nil
		}
	}

	return nil, fmt.Errorf("wallet account for %s not found", target)
}

func (c *Client) ListBankAccounts(ctx context.Context) ([]FundingBankAccount, error) {
	raw, err := c.request(ctx, http.MethodGet, "/bank_account", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("list bank accounts: %w", err)
	}
	return parseFundingBankAccounts(raw)
}

func (c *Client) GetFundingBankAccountByCurrency(ctx context.Context, currency string) (*FundingBankAccount, error) {
	accounts, err := c.ListBankAccounts(ctx)
	if err != nil {
		return nil, err
	}

	target := normalizeWalletCurrency(currency)

	for _, account := range accounts {
		if strings.EqualFold(account.SettlementCurrency, target) {
			copy := account
			return &copy, nil
		}
	}

	for _, account := range accounts {
		if strings.EqualFold(account.Currency, target) {
			copy := account
			return &copy, nil
		}
	}

	return nil, fmt.Errorf("funding bank account for %s not found", target)
}

func (c *Client) ListBanks(ctx context.Context) ([]Bank, error) {
	raw, err := c.request(ctx, http.MethodGet, "/bank", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("list banks: %w", err)
	}
	return parseBanks(raw)
}

func (c *Client) ResolveBankAccount(ctx context.Context, bankCode, accountNumber, currency string) (*ResolvedBankAccount, error) {
	if strings.TrimSpace(currency) == "" {
		currency = "NGN"
	}
	body := map[string]any{
		"currency":       strings.ToUpper(strings.TrimSpace(currency)),
		"bank_code":      strings.TrimSpace(bankCode),
		"account_number": strings.TrimSpace(accountNumber),
	}

	raw, err := c.request(ctx, http.MethodPost, "/bank/resolve/account", nil, body)
	if err != nil {
		return nil, fmt.Errorf("resolve bank account: %w", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode resolved bank account: %w", err)
	}

	source := payload
	for _, key := range []string{"bank_account", "account", "result"} {
		if nested, ok := payload[key].(map[string]any); ok {
			source = nested
			break
		}
	}

	bankSource := source
	usedNestedBank := false
	if nestedBank, ok := source["bank"].(map[string]any); ok {
		bankSource = nestedBank
		usedNestedBank = true
	}

	resolved := &ResolvedBankAccount{
		BankID:        stringValue(source, "bank_id", "id"),
		BankCode:      firstNonEmpty(stringValue(bankSource, "nip_code", "bank_code", "code"), stringValue(source, "bank_code", "code"), strings.TrimSpace(bankCode)),
		BankName:      firstNonEmpty(stringValue(bankSource, "bank_name", "name"), stringValue(source, "bank_name", "name")),
		AccountNumber: firstNonEmpty(stringValue(source, "account_number", "number"), strings.TrimSpace(accountNumber)),
		AccountName:   stringValue(source, "account_name", "beneficiary_name"),
	}
	if resolved.BankID == "" && usedNestedBank {
		resolved.BankID = stringValue(bankSource, "id", "bank_id")
	}

	if resolved.AccountName == "" {
		return nil, fmt.Errorf("resolved account name missing from provider response")
	}

	return resolved, nil
}

func (c *Client) GetRate(ctx context.Context, from, to string) (*big.Float, error) {
	source := normalizeRateCurrency(from)
	destination := strings.ToUpper(strings.TrimSpace(to))

	query := url.Values{}
	query.Set("source_currency", source)
	query.Set("destination_currency", destination)

	raw, err := c.request(ctx, http.MethodGet, "/rate", query, nil)
	if err != nil {
		return nil, fmt.Errorf("get rate: %w", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode rate response: %w", err)
	}

	keyCandidates := []string{
		fmt.Sprintf("%s-%s", destination, source),
		fmt.Sprintf("%s-%s", source, destination),
		"exchange_rate",
		"rate",
	}
	for _, key := range keyCandidates {
		if value, ok := payload[key]; ok {
			return parseBigFloat(value)
		}
	}

	if len(payload) == 1 {
		for _, value := range payload {
			return parseBigFloat(value)
		}
	}

	return nil, fmt.Errorf("rate not found in response")
}

func (c *Client) CreatePayoutDestination(ctx context.Context, req CreatePayoutDestinationRequest) (*PayoutDestination, error) {
	body := map[string]any{
		"account_id":       req.AccountID,
		"source_type":      firstNonEmpty(req.SourceType, "wallet_account"),
		"label":            req.Label,
		"type":             firstNonEmpty(req.Type, "nip"),
		"destination_type": firstNonEmpty(req.DestinationType, "bank_account"),
		"account_type":     firstNonEmpty(req.AccountType, "personal"),
		"bank_code":        req.BankCode,
		"account_number":   req.AccountNumber,
	}
	if req.BeneficiaryName != "" {
		body["beneficiary_name"] = req.BeneficiaryName
	}
	if req.BankID != "" {
		body["bank_id"] = req.BankID
	}

	raw, err := c.request(ctx, http.MethodPost, "/payout-destination", nil, body)
	if err != nil {
		return nil, fmt.Errorf("create payout destination: %w", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode payout destination: %w", err)
	}

	return &PayoutDestination{
		ID:            stringValue(payload, "id"),
		AccountName:   stringValue(payload, "account_name"),
		AccountNumber: stringValue(payload, "account_number"),
		BankName:      stringValue(payload, "bank_name"),
		BankCode:      stringValue(payload, "bank_code"),
	}, nil
}

func (c *Client) ListPayoutDestinations(ctx context.Context) ([]PayoutDestination, error) {
	raw, err := c.request(ctx, http.MethodGet, "/payout-destination", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("list payout destinations: %w", err)
	}

	objects, err := decodeObjectList(raw)
	if err != nil {
		return nil, err
	}

	destinations := make([]PayoutDestination, 0, len(objects))
	for _, payload := range objects {
		destinations = append(destinations, PayoutDestination{
			ID:            stringValue(payload, "id"),
			AccountName:   stringValue(payload, "account_name"),
			AccountNumber: stringValue(payload, "account_number"),
			BankName:      stringValue(payload, "bank_name"),
			BankCode:      stringValue(payload, "bank_code"),
		})
	}
	return destinations, nil
}

func (c *Client) CreatePayout(ctx context.Context, req CreatePayoutRequest) (*Payout, error) {
	description := firstNonEmpty(req.Remarks, req.Reference, "ConvertChain payout")
	body := map[string]any{
		"destination_id": req.DestinationID,
		"amount":         req.Amount,
		"description":    description,
	}

	raw, err := c.request(ctx, http.MethodPost, "/payout", nil, body)
	if err != nil {
		return nil, fmt.Errorf("create payout: %w", err)
	}

	return parsePayout(raw)
}

func (c *Client) FetchPayout(ctx context.Context, payoutID string) (*Payout, error) {
	raw, err := c.request(ctx, http.MethodGet, "/payout/"+url.PathEscape(payoutID), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch payout: %w", err)
	}

	return parsePayout(raw)
}

func (c *Client) MockDeposit(ctx context.Context, req MockDepositRequest) (*MockDepositResult, error) {
	body := map[string]any{
		"account_id":  req.AccountID,
		"source_type": firstNonEmpty(req.SourceType, "bank_account"),
		"currency":    strings.ToUpper(strings.TrimSpace(req.Currency)),
		"amount":      req.Amount,
		"reference":   req.Reference,
		"description": firstNonEmpty(req.Description, "ConvertChain sandbox funding"),
		"sender_name": firstNonEmpty(req.SenderName, "ConvertChain Sandbox"),
	}

	raw, err := c.request(ctx, http.MethodPost, "/deposit/mock", nil, body)
	if err != nil {
		return nil, fmt.Errorf("mock deposit: %w", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode mock deposit response: %w", err)
	}

	return &MockDepositResult{
		ID:     stringValue(payload, "id"),
		Status: stringValue(payload, "status"),
	}, nil
}

func parseBanks(raw json.RawMessage) ([]Bank, error) {
	objects, err := decodeObjectList(raw)
	if err != nil {
		return nil, err
	}

	banks := make([]Bank, 0, len(objects))
	for _, payload := range objects {
		nipCode := firstNonEmpty(stringValue(payload, "nip_code", "nipCode"), stringValue(payload, "bank_code", "code"), stringValue(payload, "bankCode"))
		shortCode := stringValue(payload, "short_code", "shortCode")
		resolveCode := firstNonEmpty(nipCode, stringValue(payload, "bank_code", "code"), stringValue(payload, "bankCode"))
		banks = append(banks, Bank{
			ID:              stringValue(payload, "id", "bank_id"),
			Code:            resolveCode,
			Name:            firstNonEmpty(stringValue(payload, "bank_name", "name"), stringValue(payload, "bankName")),
			Slug:            stringValue(payload, "slug"),
			NIPCode:         nipCode,
			ShortCode:       shortCode,
			Country:         stringValue(payload, "country"),
			Currency:        strings.ToUpper(stringValue(payload, "currency")),
			ResolveBankCode: resolveCode,
		})
	}

	return banks, nil
}

func parseWalletAccounts(raw json.RawMessage) ([]WalletAccount, error) {
	objects, err := decodeObjectList(raw)
	if err != nil {
		return nil, err
	}

	accounts := make([]WalletAccount, 0, len(objects))
	for _, payload := range objects {
		accounts = append(accounts, WalletAccount{
			ID:               stringValue(payload, "id"),
			Currency:         strings.ToUpper(stringValue(payload, "currency")),
			AccountName:      stringValue(payload, "account_name"),
			AccountNumber:    stringValue(payload, "account_number"),
			BankName:         stringValue(payload, "bank_name"),
			BankCode:         stringValue(payload, "bank_code"),
			Balance:          int64Value(payload, "balance", "amount"),
			AvailableBalance: int64Value(payload, "available_balance", "balance", "amount"),
		})
	}

	return accounts, nil
}

func parseFundingBankAccounts(raw json.RawMessage) ([]FundingBankAccount, error) {
	objects, err := decodeObjectList(raw)
	if err != nil {
		return nil, err
	}

	accounts := make([]FundingBankAccount, 0, len(objects))
	for _, payload := range objects {
		settlementCurrency := ""
		settlementWalletAccount := ""
		if settlementConfig, ok := payload["settlement_config"].(map[string]any); ok {
			settlementCurrency = strings.ToUpper(stringValue(settlementConfig, "currency"))
			settlementWalletAccount = stringValue(settlementConfig, "wallet_account_id")
		}

		accounts = append(accounts, FundingBankAccount{
			ID:                      stringValue(payload, "id"),
			Currency:                strings.ToUpper(stringValue(payload, "currency")),
			AccountName:             stringValue(payload, "account_name"),
			AccountNumber:           stringValue(payload, "account_number"),
			BankName:                stringValue(payload, "bank_name"),
			BankCode:                stringValue(payload, "bank_code"),
			SettlementCurrency:      settlementCurrency,
			SettlementWalletAccount: settlementWalletAccount,
		})
	}

	return accounts, nil
}

func parsePayout(raw json.RawMessage) (*Payout, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode payout response: %w", err)
	}

	source := payload
	if nested, ok := payload["payout"].(map[string]any); ok {
		source = nested
	}

	return &Payout{
		ID:        stringValue(source, "id"),
		Status:    strings.ToLower(stringValue(source, "status")),
		Amount:    int64Value(source, "amount"),
		Currency:  strings.ToUpper(stringValue(source, "currency")),
		CreatedAt: stringValue(source, "created_at"),
	}, nil
}

func decodeObjectList(raw json.RawMessage) ([]map[string]any, error) {
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}

	var single map[string]any
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, fmt.Errorf("decode object list: %w", err)
	}

	for _, key := range []string{"items", "results", "accounts"} {
		if nested, ok := single[key]; ok {
			nestedBytes, _ := json.Marshal(nested)
			return decodeObjectList(nestedBytes)
		}
	}

	return []map[string]any{single}, nil
}

func stringValue(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			switch typed := value.(type) {
			case string:
				return strings.TrimSpace(typed)
			case float64:
				return strconv.FormatInt(int64(typed), 10)
			case json.Number:
				return typed.String()
			}
		}
	}
	return ""
}

func int64Value(payload map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			switch typed := value.(type) {
			case float64:
				return int64(typed)
			case int64:
				return typed
			case int:
				return int64(typed)
			case string:
				if typed == "" {
					continue
				}
				parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
				if err == nil {
					return parsed
				}
			case json.Number:
				parsed, err := typed.Int64()
				if err == nil {
					return parsed
				}
			}
		}
	}
	return 0
}

func parseBigFloat(value any) (*big.Float, error) {
	switch typed := value.(type) {
	case float64:
		return big.NewFloat(typed), nil
	case string:
		rate, ok := new(big.Float).SetString(strings.TrimSpace(typed))
		if !ok {
			return nil, fmt.Errorf("invalid rate format %q", typed)
		}
		return rate, nil
	case json.Number:
		rate, ok := new(big.Float).SetString(typed.String())
		if !ok {
			return nil, fmt.Errorf("invalid rate format %q", typed.String())
		}
		return rate, nil
	default:
		return nil, fmt.Errorf("unsupported rate value type %T", value)
	}
}

func normalizeWalletCurrency(currency string) string {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "USDC", "USDT":
		return "USD"
	default:
		return strings.ToUpper(strings.TrimSpace(currency))
	}
}

func normalizeRateCurrency(currency string) string {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "USDC", "USDT":
		return "USD"
	default:
		return strings.ToUpper(strings.TrimSpace(currency))
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringifyAny(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return string(encoded)
	}
}
