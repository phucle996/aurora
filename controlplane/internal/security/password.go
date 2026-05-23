package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	passwordSaltLength = 16
	passwordMemory     = 64 * 1024
	passwordTime       = 1
	passwordThreads    = 2
	passwordKeyLength  = 32
)

var (
	// ErrInvalidPasswordHash is returned when a stored password hash is malformed.
	ErrInvalidPasswordHash = errors.New("security: invalid password hash")
)

// HashPassword hashes a password using Argon2id.
func HashPassword(password string) (string, error) {
	if strings.TrimSpace(password) == "" {
		return "", ErrInvalidClaims
	}

	salt := make([]byte, passwordSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("security: read password salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, passwordTime, passwordMemory, passwordThreads, passwordKeyLength)

	return fmt.Sprintf(
		"argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		passwordMemory,
		passwordTime,
		passwordThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword checks a plaintext password against an Argon2id encoded hash.
func VerifyPassword(encodedHash, password string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 5 {
		return false, ErrInvalidPasswordHash
	}
	if parts[0] != "argon2id" {
		return false, ErrInvalidPasswordHash
	}

	var memory uint32
	var iterations uint32
	var threads uint8

	if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false, ErrInvalidPasswordHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, ErrInvalidPasswordHash
	}

	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidPasswordHash
	}

	actualHash := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(expectedHash)))
	if subtle.ConstantTimeCompare(actualHash, expectedHash) == 1 {
		return true, nil
	}

	return false, nil
}

// PasswordParams returns the encoded parameters for audit or debugging.
func PasswordParams(encodedHash string) (map[string]int, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 5 {
		return nil, ErrInvalidPasswordHash
	}

	values := map[string]int{}
	for _, item := range strings.Split(parts[2], ",") {
		kv := strings.SplitN(item, "=", 2)
		if len(kv) != 2 {
			return nil, ErrInvalidPasswordHash
		}
		n, err := strconv.Atoi(kv[1])
		if err != nil {
			return nil, ErrInvalidPasswordHash
		}
		values[kv[0]] = n
	}

	return values, nil
}
