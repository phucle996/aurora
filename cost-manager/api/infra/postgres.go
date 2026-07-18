/*
============================================================================
MAP: COST MANAGER INFRASTRUCTURE - POSTGRESQL CONNECTOR
============================================================================
CONTRACT:
1. Nhận cấu hình tĩnh config.PsqlCfg để xây dựng DSN và kết nối pgxpool.
2. Thiết lập connection pool options (MaxConns, MinConns, MaxConnLife, MaxConnIdle).
3. Hỗ trợ cấu hình bảo mật TLS/mTLS (sslmode, sslrootcert, sslcert, sslkey).
============================================================================
*/

package infra

import (
	"context"
	"fmt"
	"os"
	"time"

	"cost-manager/api/internal/config"
	"cost-manager/api/pkg/logger"

	"github.com/jackc/pgx/v5/pgxpool"
)

// [COMMENT]: ConnectPostgres khởi tạo PostgreSQL Connection Pool từ cấu hình PsqlCfg.
func ConnectPostgres(cfg *config.PsqlCfg) (*pgxpool.Pool, error) {
	const op = "infra.postgres.connect"

	dsn := BuildDSN(cfg)
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse postgres DSN config: %w", err)
	}

	// [COMMENT]: Cấu hình Connection Pool theo các thông số cấu hình từ PsqlCfg
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

	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping postgres: %w", err)
	}

	logger.SysInfo(op, "Successfully connected to Postgres database pool")
	return pool, nil
}

// [COMMENT]: BuildDSN tạo PostgreSQL connection string từ PsqlCfg hỗ trợ đầy đủ các tham số TLS/mTLS.
func BuildDSN(cfg *config.PsqlCfg) string {
	if rawURL := os.Getenv("DATABASE_URL"); rawURL != "" {
		return rawURL
	}

	sslMode := cfg.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}

	dsn := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.DBName, sslMode,
	)

	// [COMMENT]: Cấu hình TLS / mTLS options mở rộng nếu được bật
	if cfg.TLSEnabled {
		if sslMode == "disable" {
			sslMode = "verify-full"
		}
		dsn = fmt.Sprintf(
			"postgres://%s:%s@%s:%d/%s?sslmode=%s",
			cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.DBName, sslMode,
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
	}

	return dsn
}
