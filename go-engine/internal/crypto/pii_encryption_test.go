package crypto

import (
	"crypto/rand"
	"strings"
	"testing"
)

func generateTestKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}
	return key
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	enc, err := NewPIIEncryptor(generateTestKey(t))
	if err != nil {
		t.Fatalf("NewPIIEncryptor: %v", err)
	}

	for _, tc := range []string{"22161234567", "12345678901", strings.Repeat("A", 1000)} {
		ct, err := enc.Encrypt(tc)
		if err != nil {
			t.Fatalf("Encrypt(%q): %v", tc, err)
		}
		got, err := enc.Decrypt(ct)
		if err != nil {
			t.Fatalf("Decrypt: %v", err)
		}
		if got != tc {
			t.Errorf("round-trip failed: got %q, want %q", got, tc)
		}
	}
}

// TestEncryptProducesUniqueCiphertexts ensures each encryption produces
// a different ciphertext (unique nonces). This prevents an attacker from
// identifying users who share the same BVN.
func TestEncryptProducesUniqueCiphertexts(t *testing.T) {
	enc, _ := NewPIIEncryptor(generateTestKey(t))
	ct1, _ := enc.Encrypt("22161234567")
	ct2, _ := enc.Encrypt("22161234567")
	if ct1 == ct2 {
		t.Error("same plaintext produced identical ciphertexts — nonce is not random!")
	}
}

// TestDecryptDetectsTampering verifies GCM authentication catches any modification.
func TestDecryptDetectsTampering(t *testing.T) {
	enc, _ := NewPIIEncryptor(generateTestKey(t))
	ct, _ := enc.Encrypt("22161234567")
	tampered := ct[:len(ct)-1] + "X"
	if _, err := enc.Decrypt(tampered); err == nil {
		t.Error("Decrypt should fail on tampered ciphertext")
	}
}

func TestWrongKeyFails(t *testing.T) {
	enc1, _ := NewPIIEncryptor(generateTestKey(t))
	enc2, _ := NewPIIEncryptor(generateTestKey(t))
	ct, _ := enc1.Encrypt("22161234567")
	if _, err := enc2.Decrypt(ct); err == nil {
		t.Error("Decrypt with wrong key should fail")
	}
}

func TestNewPIIEncryptorRejectsShortKey(t *testing.T) {
	if _, err := NewPIIEncryptor([]byte("too-short")); err == nil {
		t.Error("expected error for short key")
	}
}