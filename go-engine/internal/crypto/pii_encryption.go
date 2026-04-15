package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// PIIEncryptor encrypts and decrypts PII using AES-256-GCM.
// Load the key from Vault at startup (secret path: convertchain/pii_key).
type PIIEncryptor struct {
	key []byte // exactly 32 bytes
}

// NewPIIEncryptor creates a PIIEncryptor. key must be exactly 32 bytes.
func NewPIIEncryptor(key []byte) (*PIIEncryptor, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("AES-256 requires a 32-byte key; got %d bytes", len(key))
	}
	keyCopy := make([]byte, 32)
	copy(keyCopy, key)
	return &PIIEncryptor{key: keyCopy}, nil
}

// Encrypt encrypts plaintext using AES-256-GCM.
// Returns a base64-encoded string safe to store in a PostgreSQL text column.
//
// Output format: base64(nonce + ciphertext + auth_tag)
func (e *PIIEncryptor) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", errors.New("plaintext must not be empty")
	}

	// Step 1: Create AES block cipher from our 32-byte key.
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", fmt.Errorf("failed to create AES cipher: %w", err)
	}

	// Step 2: Wrap with GCM mode (authenticated encryption).
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM wrapper: %w", err)
	}

	// Step 3: Generate a cryptographically random nonce (12 bytes).
	// CRITICAL: Use crypto/rand, NOT math/rand. crypto/rand reads from the
	// OS entropy pool and is unpredictable. math/rand is deterministic.
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Step 4: Encrypt and authenticate.
	// gcm.Seal prepends the nonce to the output: [nonce][ciphertext][auth_tag]
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)

	// Step 5: Base64-encode for safe text storage.
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a base64-encoded ciphertext produced by Encrypt.
// Returns an error if the data has been tampered with or the key is wrong.
func (e *PIIEncryptor) Decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", errors.New("ciphertext must not be empty")
	}

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("failed to base64-decode ciphertext: %w", err)
	}

	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", fmt.Errorf("failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM wrapper: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize+gcm.Overhead() {
		return "", errors.New("ciphertext is too short — data may be corrupted")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]

	// gcm.Open decrypts AND verifies the authentication tag.
	// If the tag doesn't match (tampered data or wrong key), it returns an error.
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		// Generic error — don't reveal whether key or data was wrong.
		return "", errors.New("decryption failed: invalid key or tampered data")
	}

	return string(plaintext), nil
}

// EncryptIfNotEmpty encrypts plaintext only if it's non-empty.
func (e *PIIEncryptor) EncryptIfNotEmpty(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	return e.Encrypt(plaintext)
}