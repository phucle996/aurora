package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

func PresenceHMACSHA256Hex(namespace string, value string) (string, error) {
	key := GetRuntimeMasterKey()
	if len(key) != 32 {
		return "", fmt.Errorf("security: runtime master key not set or invalid")
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strings.TrimSpace(namespace)))
	mac.Write([]byte{':'})
	mac.Write([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(mac.Sum(nil)), nil
}
