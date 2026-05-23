package id

import (
	"hash/crc32"
	"strings"
)

const registerBitmapSize = 1 << 20

// NormalizeKey trims and lowercases a key before hashing.
func NormalizeKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// CRC32String returns the IEEE CRC32 checksum of a normalized string.
func CRC32String(value string) uint32 {
	return crc32.ChecksumIEEE([]byte(NormalizeKey(value)))
}

// BitmapIndex returns a stable bitmap slot in the register cache space.
func BitmapIndex(value string) int64 {
	return int64(CRC32String(value) % registerBitmapSize)
}
