package crypto

import (
	"crypto/rand"
	"math/big"
)

// GenerateAccessKey sinh Access Key ngẫu nhiên dài 20 ký tự (chuẩn uppercase và số)
func GenerateAccessKey() (string, error) {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 20)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		b[i] = charset[n.Int64()]
	}
	return string(b), nil
}

// GenerateSecretKey sinh Secret Key ngẫu nhiên dài 40 ký tự (chữ hoa thường và số)
func GenerateSecretKey() (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 40)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		b[i] = charset[n.Int64()]
	}
	return string(b), nil
}
