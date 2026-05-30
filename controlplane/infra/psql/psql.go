package psql

import (
	"context"
	"controlplane/internal/config"
	"controlplane/internal/observability"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPostgres(ctx context.Context, cfg *config.PsqlCfg) (*pgxpool.Pool, error) {
	dsn := buildDSN(cfg)

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("psql: failed to parse config: %w", err)
	}

	poolCfg.MaxConns = int32(cfg.MaxConns)
	poolCfg.MinConns = int32(cfg.MinConns)
	poolCfg.MaxConnLifetime = cfg.MaxConnLife
	poolCfg.MaxConnIdleTime = cfg.MaxConnIdle
	poolCfg.ConnConfig.Tracer = observability.NewPGXQueryTracer()

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

func buildDSN(cfg *config.PsqlCfg) string {
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.DBName, cfg.SSLMode,
	)

	if cfg.TLSEnabled {
		sslMode := strings.TrimSpace(cfg.SSLMode)
		if sslMode == "" || strings.EqualFold(sslMode, "disable") {
			sslMode = "verify-full"
		}

		dsn = fmt.Sprintf(
			"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
			cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.DBName, sslMode,
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

	searchPath := buildSearchPath(cfg.Schema)
	if searchPath != "" {
		dsn += fmt.Sprintf(" search_path=%s", searchPath)
	}

	return dsn
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
