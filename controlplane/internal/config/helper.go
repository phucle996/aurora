package config

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

func requireEnv(key string) (string, error) {
	value := getEnv(key, "")
	if value == "" {
		return "", fmt.Errorf("%s must be set and non-empty", key)
	}
	return value, nil
}

func requireEnvAsCSV(key string) ([]string, error) {
	values := getEnvAsCSV(key, nil)
	if len(values) == 0 {
		return nil, fmt.Errorf("%s must contain at least one value", key)
	}
	return values, nil
}

func requireEnvAsBool(key string) (bool, error) {
	value, err := requireEnv(key)
	if err != nil {
		return false, err
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return parsed, nil
}

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
