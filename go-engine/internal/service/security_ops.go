package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"convert-chain/go-engine/internal/domain"
	"golang.org/x/crypto/argon2"
)

const (
	txnPasswordMinLength  = 6
	txnPasswordMaxLength  = 72
	txnPasswordMaxRetries = 3
)

var txnPasswordLockDuration = 15 * time.Minute

func (s *ApplicationService) SetupTransactionPassword(ctx context.Context, userID, password string) error {
	user, err := s.getUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if !user.IsActive {
		return errors.New("account_inactive")
	}

	normalized, err := validateTransactionPassword(password)
	if err != nil {
		return err
	}

	hash, err := hashTransactionPassword(normalized)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(ctx, `
		UPDATE users
		SET txn_password_hash = $2,
			txn_password_set_at = NOW(),
			txn_password_failed_attempts = 0,
			txn_password_locked_until = NULL
		WHERE id = $1::uuid
	`, userID, hash)
	return err
}

func (s *ApplicationService) verifyTransactionPassword(ctx context.Context, user *domainUserLike, password string) error {
	if user == nil {
		return errors.New("user_not_found")
	}
	if user.Hash == nil || strings.TrimSpace(*user.Hash) == "" {
		return errors.New("transaction_password_not_set")
	}

	now := time.Now().UTC()
	if user.LockedUntil != nil && now.Before(*user.LockedUntil) {
		return errors.New("transaction_password_locked")
	}

	ok, err := verifyTransactionPasswordHash(strings.TrimSpace(*user.Hash), password)
	if err != nil {
		return err
	}

	if ok {
		if user.FailedAttempts != 0 || user.LockedUntil != nil {
			_, resetErr := s.db.Exec(ctx, `
				UPDATE users
				SET txn_password_failed_attempts = 0,
					txn_password_locked_until = NULL
				WHERE id = $1::uuid
			`, user.ID)
			if resetErr != nil {
				return resetErr
			}
		}
		return nil
	}

	nextAttempts := user.FailedAttempts + 1
	var lockedUntil *time.Time
	if nextAttempts >= txnPasswordMaxRetries {
		lock := now.Add(txnPasswordLockDuration)
		lockedUntil = &lock
	}

	_, err = s.db.Exec(ctx, `
		UPDATE users
		SET txn_password_failed_attempts = $2,
			txn_password_locked_until = $3
		WHERE id = $1::uuid
	`, user.ID, nextAttempts, lockedUntil)
	if err != nil {
		return err
	}

	if lockedUntil != nil {
		return errors.New("transaction_password_locked")
	}
	return errors.New("transaction_password_invalid")
}

type domainUserLike struct {
	ID             string
	Hash           *string
	FailedAttempts int
	LockedUntil    *time.Time
}

func buildPasswordState(user *domain.User) *domainUserLike {
	if user == nil {
		return nil
	}
	return &domainUserLike{
		ID:             user.ID.String(),
		Hash:           user.TxnPasswordHash,
		FailedAttempts: user.TxnPasswordFailedAttempts,
		LockedUntil:    user.TxnPasswordLockedUntil,
	}
}

func validateTransactionPassword(raw string) (string, error) {
	password := strings.TrimSpace(raw)
	if len(password) < txnPasswordMinLength {
		return "", fmt.Errorf("transaction password must be at least %d characters", txnPasswordMinLength)
	}
	if len(password) > txnPasswordMaxLength {
		return "", fmt.Errorf("transaction password must not exceed %d characters", txnPasswordMaxLength)
	}
	return password, nil
}

func hashTransactionPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	params := argonParams()
	hash := argon2.IDKey([]byte(password), salt, params.iterations, params.memory, params.parallelism, params.keyLen)

	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		params.memory,
		params.iterations,
		params.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func verifyTransactionPasswordHash(encodedHash string, password string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 {
		return false, errors.New("invalid transaction password hash")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("parse transaction password hash version: %w", err)
	}
	if version != argon2.Version {
		return false, errors.New("incompatible transaction password hash version")
	}

	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false, fmt.Errorf("parse transaction password hash params: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("decode transaction password salt: %w", err)
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("decode transaction password hash: %w", err)
	}

	actual := argon2.IDKey([]byte(strings.TrimSpace(password)), salt, iterations, memory, parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(expected, actual) == 1, nil
}

type argonConfiguration struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	keyLen      uint32
}

func argonParams() argonConfiguration {
	return argonConfiguration{
		memory:      64 * 1024,
		iterations:  3,
		parallelism: 2,
		keyLen:      32,
	}
}

func deletionSubjectHash(channelType, channelUserID string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(channelType)) + ":" + strings.TrimSpace(channelUserID)))
	return hex.EncodeToString(sum[:])
}

func transactionPasswordSet(user *domain.User) bool {
	return user != nil && user.TxnPasswordHash != nil && strings.TrimSpace(*user.TxnPasswordHash) != ""
}
