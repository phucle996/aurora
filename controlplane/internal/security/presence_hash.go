package security

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// PresenceHMACSHA256Hex băm namespace + value bằng SHA-256 (để giữ nguyên signature và tính tương thích ngược)
func PresenceHMACSHA256Hex(namespace string, value string) (string, error) {
	// [COMMENT]: Sử dụng SHA-256 thông thường thay vì HMAC để loại bỏ việc quản lý Master Key cho presence bitmap
	h := sha256.New()
	h.Write([]byte(strings.TrimSpace(namespace)))
	h.Write([]byte{':'})
	h.Write([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(h.Sum(nil)), nil
}
