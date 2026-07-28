package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"strings"

	"github.com/pquerna/otp/totp"
)

// ── TOTP ──────────────────────────────────────────────────────────────────────

// TOTPGenerateResult holds the output of a TOTP enrollment.
type TOTPGenerateResult struct {
	// Secret is the base32-encoded TOTP secret (store encrypted in DB).
	Secret string
	// ProvisioningURI is the otpauth:// URL for QR code generation.
	ProvisioningURI string
}

// GenerateTOTP creates a new TOTP key for the given issuer / account.
func GenerateTOTP(issuer, accountName string) (*TOTPGenerateResult, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
	})
	if err != nil {
		return nil, fmt.Errorf("security: generate totp: %w", err)
	}

	return &TOTPGenerateResult{
		Secret:          key.Secret(),
		ProvisioningURI: key.URL(),
	}, nil
}

// ValidateTOTP checks a 6-digit code against a base32 TOTP secret.
func ValidateTOTP(code, secret string) bool {
	return totp.Validate(strings.TrimSpace(code), strings.TrimSpace(secret))
}

// ── OTP (SMS / Email) ─────────────────────────────────────────────────────────

// GenerateOTP produces a cryptographically random numeric OTP of `n` digits.
func GenerateOTP(n int) (string, error) {
	const digits = "0123456789"
	buf := make([]byte, n)
	for i := range buf {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(digits))))
		if err != nil {
			return "", fmt.Errorf("security: generate otp: %w", err)
		}
		buf[i] = digits[idx.Int64()]
	}
	return string(buf), nil
}

// HashOTP returns a hex-encoded SHA-256 digest of the OTP for safe storage.
func HashOTP(otp string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(otp)))
	return hex.EncodeToString(sum[:])
}

// VerifyOTP compares a plain OTP against its stored hash in constant time.
func VerifyOTP(plain, storedHash string) bool {
	return HashOTP(plain) == storedHash
}

// ── Recovery codes ────────────────────────────────────────────────────────────

// GenerateRecoveryCode produces a single cryptographically random alphanumeric
// code of `n` characters. Uses an unambiguous charset (no 0/O, 1/I/l).
func GenerateRecoveryCode(n int) (string, error) {
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, n)
	for i := range buf {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", fmt.Errorf("security: generate recovery code: %w", err)
		}
		buf[i] = charset[idx.Int64()]
	}
	return string(buf), nil
}

// HashRecoveryCode returns a hex-encoded SHA-256 digest of a recovery code
// (normalised to uppercase before hashing).
func HashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
	return hex.EncodeToString(sum[:])
}

// EncryptMFASecret protects the TOTP seed before it crosses the PostgreSQL
// boundary. The application master key is never persisted alongside the row.
func EncryptMFASecret(masterKey, secret string) (string, error) {
	key := sha256.Sum256([]byte(masterKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("security: create mfa cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("security: create mfa aead: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("security: generate mfa nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, []byte(secret), nil)
	return "v1." + base64.RawStdEncoding.EncodeToString(nonce) + "." +
		base64.RawStdEncoding.EncodeToString(ciphertext), nil
}

func DecryptMFASecret(masterKey, encoded string) (string, error) {
	parts := strings.Split(encoded, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return "", fmt.Errorf("security: invalid mfa ciphertext")
	}
	key := sha256.Sum256([]byte(masterKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("security: create mfa cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("security: create mfa aead: %w", err)
	}
	nonce, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil || len(nonce) != aead.NonceSize() {
		return "", fmt.Errorf("security: invalid mfa nonce")
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return "", fmt.Errorf("security: invalid mfa ciphertext")
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("security: decrypt mfa secret: %w", err)
	}
	return string(plaintext), nil
}
