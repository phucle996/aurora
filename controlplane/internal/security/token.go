package security

import (
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidTokenLength = errors.New("security: invalid token length")
	ErrEmptySecret        = errors.New("security: secret cannot be empty")
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

// deterministic trong memory -> luôn không fail
func HashTokenSHA256(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return tokenEncoding.EncodeToString(sum[:])
}
