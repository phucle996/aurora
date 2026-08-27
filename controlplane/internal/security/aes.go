package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

// Encrypt mã hóa chuỗi plainText bằng khóa key (dùng AES-GCM-256)
func Encrypt(plainText string, key string) (string, error) {
	if key == "" {
		return "", errors.New("encryption key cannot be empty")
	}

	// Đảm bảo key có độ dài đúng 32 bytes bằng SHA-256
	hasher := sha256.New()
	hasher.Write([]byte(key))
	keyBytes := hasher.Sum(nil)

	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	cipherText := gcm.Seal(nonce, nonce, []byte(plainText), nil)
	return hex.EncodeToString(cipherText), nil
}

// Decrypt giải mã chuỗi hex cipherTextHex bằng khóa key (dùng AES-GCM-256)
func Decrypt(cipherTextHex string, key string) (string, error) {
	if key == "" {
		return "", errors.New("decryption key cannot be empty")
	}

	hasher := sha256.New()
	hasher.Write([]byte(key))
	keyBytes := hasher.Sum(nil)

	cipherText, err := hex.DecodeString(cipherTextHex)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(cipherText) < nonceSize {
		return "", errors.New("ciphertext too short")
	}

	nonce, actualCipherText := cipherText[:nonceSize], cipherText[nonceSize:]
	plainTextBytes, err := gcm.Open(nil, nonce, actualCipherText, nil)
	if err != nil {
		return "", err
	}

	return string(plainTextBytes), nil
}
