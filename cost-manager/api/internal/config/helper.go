/*
============================================================================
🗺️ ARCHITECTURAL COMPONENT: CONFIGURATION ENVIRONMENT HELPERS
============================================================================
CONTRACT:
1. Đọc và ép kiểu an toàn dữ liệu từ Environment Variables (OS / .env).
2. Xử lý khoảng trắng, bỏ dấu ngoặc kép bọc quanh chuỗi ký tự.
3. Cung cấp fallback default value hợp lệ cho mọi trường hợp parse lỗi.
============================================================================
*/

package config

import (
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// [COMMENT]: getEnv đọc biến môi trường dưới dạng chuỗi, loại bỏ khoảng trắng và ngoặc kép bọc ngoài nếu có.
func getEnv(key, defaultVal string) string {
	val := os.Getenv(key)
	val = strings.TrimSpace(val)
	if val == "" {
		return defaultVal
	}
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
	}
	if val == "" {
		return defaultVal
	}
	return val
}

// [COMMENT]: getEnvAsInt đọc biến môi trường và ép kiểu về integer an toàn.
func getEnvAsInt(key string, defaultVal int) int {
	val := os.Getenv(key)
	val = strings.TrimSpace(val)
	if val == "" {
		return defaultVal
	}
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
	}
	i, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return i
}

func getEnvAsInt64(key string, defaultVal int64) int64 {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return defaultVal
	}
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
	}
	i, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return defaultVal
	}
	return i
}

// [COMMENT]: getEnvAsBool đọc biến môi trường và ép kiểu về boolean an toàn.
func getEnvAsBool(key string, defaultVal bool) bool {
	val := os.Getenv(key)
	val = strings.TrimSpace(val)
	if val == "" {
		return defaultVal
	}
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
	}
	b, err := strconv.ParseBool(val)
	if err != nil {
		return defaultVal
	}
	return b
}

// [COMMENT]: getEnvAsDuration đọc biến môi trường dưới dạng duration (ví dụ: "10s", "5m").
func getEnvAsDuration(key string, defaultVal time.Duration) time.Duration {
	val := os.Getenv(key)
	val = strings.TrimSpace(val)
	if val == "" {
		return defaultVal
	}
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
	}
	d, err := time.ParseDuration(val)
	if err != nil {
		return defaultVal
	}
	return d
}

// [COMMENT]: getEnvAsCSV tách danh sách các chuỗi phân cách bởi dấu phẩy từ môi trường.
func getEnvAsCSV(key string, defaultVal []string) []string {
	val := os.Getenv(key)
	val = strings.TrimSpace(val)
	if val == "" {
		return defaultVal
	}
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
	}
	parts := strings.Split(val, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return defaultVal
	}
	return out
}

// [COMMENT]: getEnvAsFloat64 đọc biến môi trường và ép kiểu về float64.
func getEnvAsFloat64(key string, defaultVal float64) float64 {
	val := os.Getenv(key)
	val = strings.TrimSpace(val)
	if val == "" {
		return defaultVal
	}
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return defaultVal
	}
	return f
}
