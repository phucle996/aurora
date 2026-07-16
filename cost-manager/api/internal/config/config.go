package config

import "os"

type Config struct {
	Port     string
	DBURL    string
	RedisURL string
	NatsURL  string
}

func LoadConfig() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8084"
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://billing_admin:billing_secure_password@billing-psql:5432/billing?sslmode=disable"
	}

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://controlplane-acr-redis:6379"
	}

	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://controlplane-nats:4222"
	}

	return &Config{
		Port:     port,
		DBURL:    dbURL,
		RedisURL: redisURL,
		NatsURL:  natsURL,
	}
}
