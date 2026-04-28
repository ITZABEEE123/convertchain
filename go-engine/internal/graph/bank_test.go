package graph

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListBanksReturnsProviderBanks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/bank" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "bank_zenith", "name": "ZENITH BANK PLC", "slug": "zenith", "nip_code": "000015", "short_code": "057", "country": "NG", "currency": "NGN"},
			},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", false)
	client.baseURL = server.URL

	banks, err := client.ListBanks(context.Background())
	if err != nil {
		t.Fatalf("ListBanks returned error: %v", err)
	}
	if len(banks) != 1 {
		t.Fatalf("expected 1 bank, got %d", len(banks))
	}
	if banks[0].Code != "000015" || banks[0].NIPCode != "000015" || banks[0].ShortCode != "057" || banks[0].Name != "ZENITH BANK PLC" {
		t.Fatalf("unexpected bank: %+v", banks[0])
	}
}

func TestResolveBankAccountSendsCurrencyAndBankCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/bank/resolve/account" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["currency"] != "NGN" {
			t.Fatalf("expected currency NGN, got %#v", body["currency"])
		}
		if body["bank_code"] != "000015" {
			t.Fatalf("expected bank_code 000015, got %#v", body["bank_code"])
		}
		if _, ok := body["bank_name"]; ok {
			t.Fatalf("bank_name must not be sent to Graph resolve")
		}
		if body["account_number"] != "2274091001" {
			t.Fatalf("unexpected account number")
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"account_number": "2274091001",
				"account_name":   "TEST USER",
				"bank": map[string]any{
					"id":       "bank_zenith",
					"name":     "ZENITH BANK PLC",
					"nip_code": "000015",
				},
			},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", false)
	client.baseURL = server.URL

	resolved, err := client.ResolveBankAccount(context.Background(), "000015", "2274091001", "NGN")
	if err != nil {
		t.Fatalf("ResolveBankAccount returned error: %v", err)
	}
	if resolved.AccountName != "TEST USER" || resolved.BankCode != "000015" || resolved.BankName != "ZENITH BANK PLC" {
		t.Fatalf("unexpected resolution: %+v", resolved)
	}
}

func TestResolveBankAccountMapsProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "invalid_account",
				"message": "Invalid account number",
			},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", false)
	client.baseURL = server.URL

	_, err := client.ResolveBankAccount(context.Background(), "057", "2274091001", "NGN")
	if err == nil {
		t.Fatal("expected error")
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if providerErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected HTTP 400, got %d", providerErr.StatusCode)
	}
	if providerErr.Code != "invalid_account" {
		t.Fatalf("expected provider code invalid_account, got %q", providerErr.Code)
	}
}
