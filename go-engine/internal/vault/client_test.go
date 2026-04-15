package vault

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetSecret(t *testing.T) {
	mockResponse := map[string]interface{}{
		"data": map[string]interface{}{
			"data": map[string]interface{}{
				"api_key": "test-binance-key",
			},
		},
	}

	// httptest.NewServer creates a local HTTP server for testing.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "test-token" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	client := New(server.URL, "test-token", "secret")

	key, err := client.GetSecret(context.Background(), "convertchain/binance", "api_key")
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if key != "test-binance-key" {
		t.Errorf("got %q, want %q", key, "test-binance-key")
	}
}

func TestGetSecret_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := New(server.URL, "test-token", "secret")
	_, err := client.GetSecret(context.Background(), "nonexistent/path", "field")
	if err == nil {
		t.Error("expected error for 404 response, got nil")
	}
}