package security

import (
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidTokenLength = errors.New("security: invalid token length")
)

var tokenEncoding = base64.RawURLEncoding

func GenerateToken(length int) (string, error) {
	if length <= 0 {
		return "", ErrInvalidTokenLength
	}

	var builder strings.Builder
	builder.Grow(length)

	for builder.Len() < length {
		block := make([]byte, 32)
		if _, err := cryptorand.Read(block); err != nil {
			return "", fmt.Errorf("security: read token entropy: %w", err)
		}
		builder.WriteString(tokenEncoding.EncodeToString(block))
	}

	token := builder.String()
	if len(token) > length {
		token = token[:length]
	}
	return token, nil
}

func HashToken(token, secret string) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", ErrEmptySecret
	}
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write([]byte(strings.TrimSpace(token))); err != nil {
		return "", fmt.Errorf("security: hash token: %w", err)
	}
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// deterministic trong memory -> luôn không fail
func HashTokenSHA256(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return tokenEncoding.EncodeToString(sum[:])
}
