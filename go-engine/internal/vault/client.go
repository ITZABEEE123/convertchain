package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a minimal HashiCorp Vault KV v2 HTTP client.
type Client struct {
	baseURL    string
	token      string
	mountPath  string
	httpClient *http.Client
}

// New creates a Vault client.
//   baseURL:   "http://127.0.0.1:8200"
//   token:     from VAULT_TOKEN env variable
//   mountPath: "secret" (the KV v2 mount)
func New(baseURL, token, mountPath string) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     token,
		mountPath: mountPath,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

type kvv2Response struct {
	Data struct {
		Data     map[string]interface{} `json:"data"`
		Metadata struct{ Version int `json:"version"` } `json:"metadata"`
	} `json:"data"`
	Errors []string `json:"errors"`
}

// GetSecret fetches a single field from a Vault KV v2 secret.
//
// Example:
//   key, err := vault.GetSecret(ctx, "convertchain/binance", "api_key")
func (c *Client) GetSecret(ctx context.Context, path, field string) (string, error) {
	// Vault KV v2 URL: GET /v1/{mount}/data/{path}
	url := fmt.Sprintf("%s/v1/%s/data/%s", c.baseURL, c.mountPath, path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to build vault request: %w", err)
	}
	req.Header.Set("X-Vault-Token", c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("vault request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusNotFound:
		return "", fmt.Errorf("secret not found at path: %s", path)
	case http.StatusForbidden:
		return "", fmt.Errorf("vault: permission denied for path: %s", path)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("vault returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var vaultResp kvv2Response
	if err := json.Unmarshal(body, &vaultResp); err != nil {
		return "", fmt.Errorf("failed to parse vault response: %w", err)
	}
	if len(vaultResp.Errors) > 0 {
		return "", fmt.Errorf("vault error: %s", strings.Join(vaultResp.Errors, "; "))
	}

	value, ok := vaultResp.Data.Data[field]
	if !ok {
		return "", fmt.Errorf("field %q not found at path %s", field, path)
	}

	strValue, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("field %q at path %s is not a string", field, path)
	}

	return strValue, nil
}

// GetSecretMap fetches all fields of a secret as a map[string]string.
func (c *Client) GetSecretMap(ctx context.Context, path string) (map[string]string, error) {
	url := fmt.Sprintf("%s/v1/%s/data/%s", c.baseURL, c.mountPath, path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build vault request: %w", err)
	}
	req.Header.Set("X-Vault-Token", c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vault request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vault returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var vaultResp kvv2Response
	if err := json.Unmarshal(body, &vaultResp); err != nil {
		return nil, fmt.Errorf("failed to parse vault response: %w", err)
	}

	result := make(map[string]string, len(vaultResp.Data.Data))
	for k, v := range vaultResp.Data.Data {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	return result, nil
}