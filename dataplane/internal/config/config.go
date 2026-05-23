package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	App AppCfg
}

type AppCfg struct {
	Name            string
	LogLevel        string
	ShutdownTimeout time.Duration
}

func Load() *Config {
	return &Config{
		App: AppCfg{
			Name:            envString("APP_NAME", "aurora-dataplane"),
			LogLevel:        envString("APP_LOG_LEVEL", "info"),
			ShutdownTimeout: envDuration("APP_SHUTDOWN_TIMEOUT", 15*time.Second),
		},
	}
}

func envString(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	if parsed, err := time.ParseDuration(value); err == nil {
		return parsed
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}
