package storage

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"controlplane/internal/config"
	storagemigrations "controlplane/internal/storage/migrations"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var storageSchemaIdentPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// [COMMENT]: ApplyMigrations thực thi khởi tạo schema cho phân hệ Storage.
// Sử dụng advisory lock (id: 1102) để đảm bảo đồng bộ hóa chạy migrations HA.
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

	// [COMMENT]: Áp dụng các file migration SQL nhúng tự động
	if err := applyEmbeddedMigrations(ctx, conn, schema, storagemigrations.Files); err != nil {
		return err
	}

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

func applyEmbeddedMigrations(ctx context.Context, conn *pgxpool.Conn, module string, files fs.FS) error {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return fmt.Errorf("storage migration: read embedded directories for %s: %w", module, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		queryBytes, err := fs.ReadFile(files, name)
		if err != nil {
			return fmt.Errorf("storage migration: read file %s/%s failed: %w", module, name, err)
		}
		query := string(queryBytes)
		if strings.TrimSpace(query) == "" {
			continue
		}
		// [COMMENT]: Sử dụng QueryExecModeSimpleProtocol để cho phép chạy script SQL nhiều câu lệnh (multi-statement)
		if _, err := conn.Exec(ctx, query, pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("storage migration: execute script %s/%s failed: %w", module, name, err)
		}
	}

	return nil
}
