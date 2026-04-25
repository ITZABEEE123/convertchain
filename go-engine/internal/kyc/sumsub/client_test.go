package sumsub

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifyWebhookSignatureSupportsConfiguredAlgorithms(t *testing.T) {
	payload := []byte(`{"type":"applicantReviewed"}`)
	secret := "webhook-secret"

	sha256Digest := hmacDigest(sha256.New, secret, payload)
	sha512Digest := hmacDigest(sha512.New, secret, payload)
	client := NewClient("app-token", "secret-key", false)

	if !client.VerifyWebhookSignature(payload, sha256Digest, "HMAC_SHA256_HEX", secret) {
		t.Fatal("expected SHA-256 webhook digest to verify")
	}
	if !client.VerifyWebhookSignature(payload, sha512Digest, "HMAC_SHA512_HEX", secret) {
		t.Fatal("expected SHA-512 webhook digest to verify")
	}
	if client.VerifyWebhookSignature(payload, sha256Digest, "HMAC_SHA512_HEX", secret) {
		t.Fatal("expected digest with wrong algorithm to fail")
	}
	if client.VerifyWebhookSignature(payload, "bad-digest", "HMAC_SHA256_HEX", secret) {
		t.Fatal("expected invalid digest to fail")
	}
}

func TestCreateWebSDKLinkReturnsURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/resources/sdkIntegrations/levels/-/websdkLink" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("X-App-Token") == "" || r.Header.Get("X-App-Access-Sig") == "" || r.Header.Get("X-App-Access-Ts") == "" {
			t.Fatal("missing Sumsub auth headers")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"https://api.sumsub.com/websdk/p/test"}`))
	}))
	defer server.Close()

	client := NewClient("app-token", "secret-key", false)
	client.baseURL = server.URL

	link, err := client.CreateWebSDKLink(context.Background(), WebSDKLinkRequest{
		UserID:      "user-1",
		LevelName:   "telegram-tier1",
		PhoneNumber: "+2348012345678",
		TTLInSecs:   600,
	})
	if err != nil {
		t.Fatalf("CreateWebSDKLink returned error: %v", err)
	}
	if link.URL != "https://api.sumsub.com/websdk/p/test" {
		t.Fatalf("unexpected link URL: %s", link.URL)
	}
}

func hmacDigest(hashFactory func() hash.Hash, secret string, payload []byte) string {
	mac := hmac.New(hashFactory, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
