package storage

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"controlplane/internal/config"

	"github.com/jackc/pgx/v5/pgxpool"
)

var storageSchemaIdentPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// [COMMENT]: ApplyMigrations thực thi khởi tạo schema cho phân hệ Storage.
func ApplyMigrations(ctx context.Context, conn *pgxpool.Conn, cfg *config.Config) error {
	if conn == nil {
		return fmt.Errorf("storage migration: connection is nil")
	}
	if cfg == nil {
		return fmt.Errorf("storage migration: config is nil")
	}
	schema := strings.TrimSpace(cfg.SchemaSQL.Storage)

	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		return fmt.Errorf("storage migration: begin tx: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(ctx, "ROLLBACK")
	}()

	// [COMMENT]: Advisory lock tránh race condition khi nhiều replica pod chạy migration cùng lúc
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_xact_lock(1102)"); err != nil {
		return fmt.Errorf("storage migration: acquire advisory lock: %w", err)
	}

	if err := ensureMigrationSchema(ctx, conn, schema); err != nil {
		return err
	}
	if err := setMigrationSearchPath(ctx, conn, schema+",public"); err != nil {
		return err
	}

	// [COMMENT]: SKELETON - Khi có file migrations .sql sẽ gọi hàm apply các file ở đây.

	if _, err := conn.Exec(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("storage migration: commit tx: %w", err)
	}
	return nil
}

func ensureMigrationSchema(ctx context.Context, conn *pgxpool.Conn, schema string) error {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		return fmt.Errorf("storage migration: schema is required")
	}
	if !storageSchemaIdentPattern.MatchString(schema) {
		return fmt.Errorf("storage migration: invalid schema name %q", schema)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema)); err != nil {
		return fmt.Errorf("storage migration: create schema %s: %w", schema, err)
	}
	return nil
}

func setMigrationSearchPath(ctx context.Context, conn *pgxpool.Conn, searchPath string) error {
	searchPath = strings.TrimSpace(searchPath)
	if searchPath == "" {
		return fmt.Errorf("storage migration: search_path is required")
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %s", searchPath)); err != nil {
		return fmt.Errorf("storage migration: set search_path to %s: %w", searchPath, err)
	}
	return nil
}
