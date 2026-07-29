package redis

import (
	"context"
	"controlplane/infra/vault"
	"controlplane/internal/config"
	"controlplane/internal/observability"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const (
	SharedConnectionPath    = "secret/data/connections/redis/shared-l2/role-request-reply-rw"
	AuthStateConnectionPath = "secret/data/connections/redis/auth-state/role-authz-projection-rw"
)

type connectionRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Addr          string `json:"addr"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	DB            int    `json:"db"`
	TLSEnabled    bool   `json:"tls_enabled"`
	CACertPath    string `json:"ca_cert_path"`
	CertPath      string `json:"cert_path"`
	KeyPath       string `json:"key_path"`
}

func NewRedis(ctx context.Context, vaultClient *vault.Client, cfg *config.RedisCfg, connectionPath string) (*goredis.Client, error) {
	var connection connectionRecord
	if err := vaultClient.ReadJSON(ctx, connectionPath, &connection); err != nil {
		return nil, fmt.Errorf("redis: read Vault connection record: %w", err)
	}
	if err := validateConnection(connection, connectionPath); err != nil {
		return nil, err
	}
	opts := &goredis.Options{
		Addr:         connection.Addr,
		Username:     connection.Username,
		Password:     connection.Password,
		DB:           connection.DB,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
		// [COMMENT]: Blocking durability commands phải tôn trọng deadline của workflow,
		// không giữ pooled connection vô hạn khi replica hoặc AOF stall.
		ContextTimeoutEnabled: true,
	}

	if connection.TLSEnabled {
		tlsCfg, err := buildTLSConfig(connection)
		if err != nil {
			return nil, fmt.Errorf("redis: failed to build TLS config: %w", err)
		}
		opts.TLSConfig = tlsCfg
	}

	var rdb *goredis.Client
	var lastErr error

	for attempt := 1; attempt <= cfg.MaxRetries; attempt++ {
		rdb = goredis.NewClient(opts)
		rdb.AddHook(observability.NewRedisHook())

		pingCtx, pingCancel := context.WithTimeout(ctx, cfg.PingTimeout)
		lastErr = rdb.Ping(pingCtx).Err()
		pingCancel()

		if lastErr == nil {
			return rdb, nil
		}

		_ = rdb.Close()

		if attempt < cfg.MaxRetries {
			time.Sleep(cfg.RetryInterval)
		}
	}

	return nil, fmt.Errorf("redis: failed to connect after %d attempts: %w", cfg.MaxRetries, lastErr)
}

func buildTLSConfig(cfg connectionRecord) (*tls.Config, error) {
	tlsCfg := &tls.Config{}

	host := cfg.Addr
	if parsedHost, _, err := net.SplitHostPort(cfg.Addr); err == nil {
		host = parsedHost
	}
	tlsCfg.ServerName = strings.TrimSpace(host)

	if cfg.CACertPath != "" {
		caCert, err := os.ReadFile(cfg.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("redis: failed to read CA cert: %w", err)
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caCert)
		tlsCfg.RootCAs = pool
	}

	if cfg.CertPath != "" && cfg.KeyPath != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertPath, cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("redis: failed to load client cert/key: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return tlsCfg, nil
}

func validateConnection(value connectionRecord, path string) error {
	if value.SchemaVersion != 1 {
		return fmt.Errorf("redis: unsupported Vault schema_version %d", value.SchemaVersion)
	}
	if strings.TrimSpace(value.Addr) == "" {
		return fmt.Errorf("redis: Vault connection address is required")
	}
	if value.DB < 0 {
		return fmt.Errorf("redis: Vault database index is invalid")
	}
	if strings.Contains(path, "auth-state") && value.DB != 0 {
		return fmt.Errorf("redis: Auth-State Redis must use database index 0")
	}
	return nil
}
