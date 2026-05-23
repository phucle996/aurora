package testutil

import (
	"encoding/base64"
	"fmt"
	"strings"
)

func decodeRuntimeMasterKey(encoded string) ([]byte, error) {
	value := strings.TrimSpace(encoded)
	if value == "" {
		return nil, fmt.Errorf("missing runtime master key")
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf("invalid base64 runtime master key")
		}
	}
	if len(decoded) != 32 {
		return nil, fmt.Errorf("runtime master key must decode to 32 bytes")
	}
	return decoded, nil
}
