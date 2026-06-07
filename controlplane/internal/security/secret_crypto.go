package security

import (
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

var runtimeMasterKey []byte

func SetRuntimeMasterKey(key []byte) {
	if len(key) == 0 {
		runtimeMasterKey = nil
		return
	}
	runtimeMasterKey = append([]byte(nil), key...)
}

func GetRuntimeMasterKey() []byte {
	if len(runtimeMasterKey) == 0 {
		return nil
	}
	return append([]byte(nil), runtimeMasterKey...)
}

func EncryptSecret(plainText string) (string, error) {
	return EncryptSecretBytes([]byte(plainText))
}

func EncryptSecretBytes(plainText []byte) (string, error) {
	if len(runtimeMasterKey) != 32 {
		return "", fmt.Errorf("security: runtime master key not set or invalid")
	}
	block, err := aes.NewCipher(runtimeMasterKey)
	if err != nil {
		return "", fmt.Errorf("security: create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("security: create gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(cryptorand.Reader, nonce); err != nil {
		return "", fmt.Errorf("security: read nonce: %w", err)
	}

	cipherText := gcm.Seal(nil, nonce, plainText, nil)
	payload := append(nonce, cipherText...)
	return base64.RawStdEncoding.EncodeToString(payload), nil
}

func DecryptSecret(cipherPayload string) (string, error) {
	plain, err := DecryptSecretBytes(cipherPayload)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func DecryptSecretBytes(cipherPayload string) ([]byte, error) {
	if len(runtimeMasterKey) != 32 {
		return nil, fmt.Errorf("security: runtime master key not set or invalid")
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(cipherPayload))
	if err != nil {
		return nil, fmt.Errorf("security: decode encrypted secret: %w", err)
	}

	block, err := aes.NewCipher(runtimeMasterKey)
	if err != nil {
		return nil, fmt.Errorf("security: create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("security: create gcm: %w", err)
	}

	if len(raw) < gcm.NonceSize() {
		return nil, fmt.Errorf("security: encrypted secret payload too short")
	}

	nonce := raw[:gcm.NonceSize()]
	cipherText := raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, cipherText, nil)
	if err != nil {
		return nil, fmt.Errorf("security: decrypt secret: %w", err)
	}

	return plain, nil
}
