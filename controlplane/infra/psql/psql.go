package psql

import (
	"context"
	"controlplane/infra/vault"
	"controlplane/internal/config"
	"controlplane/internal/observability"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const connectionPath = "secret/data/connections/postgres/pg-central/role-business-rw"

type connectionRecord struct {
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

func NewPostgres(
	ctx context.Context,
	vaultClient *vault.Client,
	cfg *config.PsqlCfg,
	metrics observability.DependencyRecorder,
) (*pgxpool.Pool, error) {
	var connection connectionRecord
	if err := vaultClient.ReadJSON(ctx, connectionPath, &connection); err != nil {
		return nil, fmt.Errorf("psql: read Vault connection record: %w", err)
	}
	if err := validateConnection(connection); err != nil {
		return nil, err
	}
	dsn := buildDSN(connection)

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("psql: failed to parse config: %w", err)
	}

	poolCfg.MaxConns = int32(cfg.MaxConns)
	poolCfg.MinConns = int32(cfg.MinConns)
	poolCfg.MaxConnLifetime = cfg.MaxConnLife
	poolCfg.MaxConnIdleTime = cfg.MaxConnIdle
	poolCfg.ConnConfig.Tracer = observability.NewPGXQueryTracer(metrics)

	var pool *pgxpool.Pool

	for attempt := 1; attempt <= cfg.MaxRetries; attempt++ {
		pool, err = pgxpool.NewWithConfig(ctx, poolCfg)
		if err != nil {
			if attempt < cfg.MaxRetries {
				time.Sleep(cfg.RetryInterval)
			}
			continue
		}

		pingCtx, pingCancel := context.WithTimeout(ctx, cfg.PingTimeout)
		err = pool.Ping(pingCtx)
		pingCancel()

		if err != nil {
			pool.Close()
			if attempt < cfg.MaxRetries {
				time.Sleep(cfg.RetryInterval)
			}
			continue
		}

		return pool, nil
	}

	return nil, fmt.Errorf("psql: failed to connect after %d attempts: %w", cfg.MaxRetries, sanitizeError(err))
}

func sanitizeError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if idx := strings.Index(msg, "server error:"); idx != -1 {
		return fmt.Errorf("db error: %s", strings.TrimSpace(msg[idx:]))
	}
	if idx := strings.Index(msg, "FATAL:"); idx != -1 {
		return fmt.Errorf("db error: %s", strings.TrimSpace(msg[idx:]))
	}
	if strings.Contains(msg, "failed to connect to") {
		return fmt.Errorf("db connection failure")
	}
	return err
}

func buildDSN(cfg connectionRecord) string {
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.Username, cfg.Password, cfg.Database, cfg.SSLMode,
	)

	if cfg.TLSEnabled {
		sslMode := strings.TrimSpace(cfg.SSLMode)
		if sslMode == "" || strings.EqualFold(sslMode, "disable") {
			sslMode = "verify-full"
		}

		dsn = fmt.Sprintf(
			"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
			cfg.Host, cfg.Port, cfg.Username, cfg.Password, cfg.Database, sslMode,
		)

		if cfg.CACertPath != "" {
			dsn += fmt.Sprintf(" sslrootcert=%s", cfg.CACertPath)
		}
		if cfg.CertPath != "" {
			dsn += fmt.Sprintf(" sslcert=%s", cfg.CertPath)
		}
		if cfg.KeyPath != "" {
			dsn += fmt.Sprintf(" sslkey=%s", cfg.KeyPath)
		}
	}

	return dsn
}

func validateConnection(value connectionRecord) error {
	if value.SchemaVersion != 1 {
		return fmt.Errorf("psql: unsupported Vault schema_version %d", value.SchemaVersion)
	}
	if strings.TrimSpace(value.Host) == "" ||
		strings.TrimSpace(value.Username) == "" ||
		strings.TrimSpace(value.Password) == "" ||
		strings.TrimSpace(value.Database) == "" {
		return fmt.Errorf("psql: Vault connection record is incomplete")
	}
	if value.Port < 1 || value.Port > 65535 {
		return fmt.Errorf("psql: Vault PostgreSQL port is out of range")
	}
	switch strings.ToLower(strings.TrimSpace(value.SSLMode)) {
	case "disable", "require", "verify-ca", "verify-full":
	default:
		return fmt.Errorf("psql: Vault PostgreSQL ssl_mode %q is unsupported", value.SSLMode)
	}
	if value.TLSEnabled && strings.EqualFold(value.SSLMode, "disable") {
		return fmt.Errorf("psql: Vault TLS connection cannot use ssl_mode=disable")
	}
	return nil
}

func buildSearchPath(value string) string {
	parts := strings.Split(strings.TrimSpace(value), ",")
	seen := make(map[string]struct{}, len(parts)+1)
	ordered := make([]string, 0, len(parts)+1)
	appendSchema := func(item string) {
		item = strings.TrimSpace(item)
		if item == "" {
			return
		}
		if _, ok := seen[item]; ok {
			return
		}
		seen[item] = struct{}{}
		ordered = append(ordered, item)
	}
	for _, part := range parts {
		appendSchema(part)
	}
	if len(ordered) == 0 {
		appendSchema("iam")
	}
	appendSchema("public")
	return strings.Join(ordered, ",")
}
