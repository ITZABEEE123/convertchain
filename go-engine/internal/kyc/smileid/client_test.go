package smileid

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLookupBVNUsesVerifyEndpointAndParsesVerifiedResponse(t *testing.T) {
	t.Parallel()

	var captured map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/verify" {
			t.Fatalf("expected /verify path, got %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"FullName":    "John Doe",
			"DOB":         "1990-01-15",
			"PhoneNumber": "08012345678",
			"ResultCode":  "1020",
			"ResultText":  "Exact Match",
			"Actions": map[string]interface{}{
				"Verify_ID_Number": "Verified",
				"Names":            "Exact Match",
				"DOB":              "Exact Match",
				"Phone_Number":     "Exact Match",
			},
		})
	}))
	defer server.Close()

	client := NewClient("085", "test-api-key", true)
	client.baseURL = server.URL

	result, err := client.LookupBVN(context.Background(), BVNLookupRequest{
		BVN:         "00000000000",
		FirstName:   "John",
		LastName:    "Doe",
		DateOfBirth: "1990-01-15",
		PhoneNumber: "08012345678",
	})
	if err != nil {
		t.Fatalf("expected lookup to succeed, got error: %v", err)
	}
	if !result.Verified {
		t.Fatalf("expected verified lookup result")
	}
	if !result.NameMatch || !result.DOBMatch || !result.PhoneMatch {
		t.Fatalf("expected all match flags to be true: %#v", result)
	}
	if result.Status != "VALID" {
		t.Fatalf("expected VALID status, got %s", result.Status)
	}

	if captured["source_sdk"] != basicKYCSourceSDK {
		t.Fatalf("expected source_sdk %q, got %#v", basicKYCSourceSDK, captured["source_sdk"])
	}
	if captured["id_type"] != "BVN" {
		t.Fatalf("expected id_type BVN, got %#v", captured["id_type"])
	}

	partnerParams, ok := captured["partner_params"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected partner_params object, got %#v", captured["partner_params"])
	}
	if partnerParams["user_id"] == "" || partnerParams["job_id"] == "" {
		t.Fatalf("expected partner_params job_id and user_id to be populated: %#v", partnerParams)
	}
	if partnerParams["job_type"] != float64(basicKYCJobType) {
		t.Fatalf("expected job_type %d, got %#v", basicKYCJobType, partnerParams["job_type"])
	}
}

func TestLookupNINMapsUnsupportedProductResult(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ResultCode": "1016",
			"ResultText": "Unable to verify ID - This feature is not enabled for your Smile ID account.",
			"Actions": map[string]interface{}{
				"Verify_ID_Number": "Not Done",
			},
		})
	}))
	defer server.Close()

	client := NewClient("085", "test-api-key", true)
	client.baseURL = server.URL

	result, err := client.LookupNIN(context.Background(), NINLookupRequest{
		NIN:         "00000000000",
		FirstName:   "John",
		LastName:    "Doe",
		DateOfBirth: "1990-01-15",
		PhoneNumber: "08012345678",
	})
	if err != nil {
		t.Fatalf("expected lookup to succeed, got error: %v", err)
	}
	if result.Status != "UNSUPPORTED" {
		t.Fatalf("expected UNSUPPORTED status, got %s", result.Status)
	}
	if result.Verified {
		t.Fatalf("expected unsupported lookup to be unverified")
	}
	if result.Reason == "" {
		t.Fatalf("expected a human-readable unsupported reason")
	}
}
