/*
============================================================================
MAP: COST MANAGER INFRASTRUCTURE - POSTGRESQL CONNECTOR
============================================================================
CONTRACT:
1. Đọc connection record của billing PostgreSQL trực tiếp từ Vault.
2. Giữ pool policy trong config; credential không đi qua application config.
3. Validate toàn bộ record ngay tại Vault/connector boundary trước khi dial.
============================================================================
*/

package infra

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cost-manager/api/internal/config"
	"cost-manager/api/pkg/logger"

	"github.com/jackc/pgx/v5/pgxpool"
)

const postgresConnectionPath = "secret/data/connections/postgres/pg-billing/role-ledger-rw"

type postgresConnectionRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	Database      string `json:"database"`
	SSLMode       string `json:"ssl_mode"`
	TLSEnabled    bool   `json:"tls_enabled"`
	CACertPath    string `json:"ca_cert_path"`
	CertPath      string `json:"cert_path"`
	KeyPath       string `json:"key_path"`
}

// ConnectPostgres reads the fixed billing capability record at the connector
// boundary and then builds the durable pool.
func ConnectPostgres(ctx context.Context, vaultClient *VaultClient, cfg *config.PsqlCfg) (*pgxpool.Pool, error) {
	const op = "infra.postgres.connect"

	var connection postgresConnectionRecord
	if err := vaultClient.ReadJSON(ctx, postgresConnectionPath, &connection); err != nil {
		return nil, fmt.Errorf("read Vault postgres connection: %w", err)
	}
	if err := validatePostgresConnection(connection); err != nil {
		return nil, err
	}

	dsn := BuildDSN(connection)
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse postgres DSN config: %w", err)
	}
	poolConfig.MaxConns = int32(cfg.MaxConns)
	poolConfig.MinConns = int32(cfg.MinConns)

	if maxIdle, err := time.ParseDuration(cfg.MaxConnIdle); err == nil {
		poolConfig.MaxConnIdleTime = maxIdle
	} else {
		poolConfig.MaxConnIdleTime = 15 * time.Minute
	}
	if maxLife, err := time.ParseDuration(cfg.MaxConnLife); err == nil {
		poolConfig.MaxConnLifetime = maxLife
	} else {
		poolConfig.MaxConnLifetime = 30 * time.Minute
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping postgres: %w", err)
	}
	logger.SysInfo(op, "Successfully connected to Postgres database pool")
	return pool, nil
}

func BuildDSN(cfg postgresConnectionRecord) string {
	sslMode := strings.TrimSpace(cfg.SSLMode)
	if sslMode == "" {
		sslMode = "disable"
	}
	if cfg.TLSEnabled && sslMode == "disable" {
		sslMode = "verify-full"
	}

	dsn := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.Database, sslMode,
	)
	if cfg.CACertPath != "" {
		dsn += fmt.Sprintf("&sslrootcert=%s", cfg.CACertPath)
	}
	if cfg.CertPath != "" {
		dsn += fmt.Sprintf("&sslcert=%s", cfg.CertPath)
	}
	if cfg.KeyPath != "" {
		dsn += fmt.Sprintf("&sslkey=%s", cfg.KeyPath)
	}
	return dsn
}

func validatePostgresConnection(value postgresConnectionRecord) error {
	if value.SchemaVersion != 1 {
		return fmt.Errorf("infra postgres: unsupported Vault schema_version %d", value.SchemaVersion)
	}
	if strings.TrimSpace(value.Host) == "" ||
		strings.TrimSpace(value.Username) == "" ||
		strings.TrimSpace(value.Password) == "" ||
		strings.TrimSpace(value.Database) == "" {
		return fmt.Errorf("infra postgres: Vault connection record is incomplete")
	}
	if value.Port < 1 || value.Port > 65535 {
		return fmt.Errorf("infra postgres: Vault PostgreSQL port is out of range")
	}
	switch strings.ToLower(strings.TrimSpace(value.SSLMode)) {
	case "disable", "require", "verify-ca", "verify-full":
	default:
		return fmt.Errorf("infra postgres: unsupported Vault ssl_mode %q", value.SSLMode)
	}
	return nil
}
