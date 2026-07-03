package hypervisor

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"controlplane/internal/config"
	hypervisormigrations "controlplane/internal/hypervisor/migrations"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var hypervisorSchemaIdentPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ApplyMigrations tự động tạo schema và thực thi toàn bộ các bản nâng cấp DB SQL cho module Hypervisor.
// Luồng sử dụng advisory lock (id: 1104) để đảm bảo an toàn HA khi nhiều node khởi chạy cùng lúc.
func ApplyMigrations(ctx context.Context, conn *pgxpool.Conn, cfg *config.Config) error {
	// [COMMENT]: Bắt đầu một transaction cục bộ cho migration của module
	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		return fmt.Errorf("hypervisor migration: begin transaction: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(ctx, "ROLLBACK")
	}()

	// [COMMENT]: Advisory lock ở cấp độ transaction để đồng bộ hóa migration của Hypervisor
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_xact_lock(1104)"); err != nil {
		return fmt.Errorf("hypervisor migration: acquire advisory lock: %w", err)
	}

	// [COMMENT]: Đảm bảo schema tồn tại
	if err := ensureMigrationSchema(ctx, conn, cfg.SchemaSQL.Hypervisor); err != nil {
		return err
	}

	// [COMMENT]: Thiết lập search_path để các câu lệnh SQL viết trong file .up.sql tự động ghi nhận vào schema tương ứng
	if err := setMigrationSearchPath(ctx, conn, cfg.SchemaSQL.Hypervisor+",public"); err != nil {
		return err
	}

	// [COMMENT]: Thực thi tuần tự các file SQL embedded
	if err := applyEmbeddedMigrations(ctx, conn, cfg.SchemaSQL.Hypervisor, hypervisormigrations.Files); err != nil {
		return err
	}

	// [COMMENT]: Commit transaction nếu tất cả tệp migrations chạy thành công
	if _, err := conn.Exec(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("hypervisor migration: commit transaction: %w", err)
	}
	return nil
}

func ensureMigrationSchema(ctx context.Context, conn *pgxpool.Conn, schema string) error {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		return fmt.Errorf("hypervisor migration: schema name is empty")
	}
	if !hypervisorSchemaIdentPattern.MatchString(schema) {
		return fmt.Errorf("hypervisor migration: invalid schema identifier %q", schema)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema)); err != nil {
		return fmt.Errorf("hypervisor migration: create schema %s failed: %w", schema, err)
	}
	return nil
}

func setMigrationSearchPath(ctx context.Context, conn *pgxpool.Conn, searchPath string) error {
	searchPath = strings.TrimSpace(searchPath)
	if searchPath == "" {
		return fmt.Errorf("hypervisor migration: search_path cannot be empty")
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %s", searchPath)); err != nil {
		return fmt.Errorf("hypervisor migration: set search_path to %s failed: %w", searchPath, err)
	}
	return nil
}

func applyEmbeddedMigrations(ctx context.Context, conn *pgxpool.Conn, module string, files fs.FS) error {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return fmt.Errorf("hypervisor migration: read embedded directories for %s: %w", module, err)
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
			return fmt.Errorf("hypervisor migration: read file %s/%s failed: %w", module, name, err)
		}
		query := string(queryBytes)
		if strings.TrimSpace(query) == "" {
			continue
		}
		// Sử dụng QueryExecModeSimpleProtocol để cho phép chạy script SQL nhiều câu lệnh (multi-statement)
		if _, err := conn.Exec(ctx, query, pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("hypervisor migration: execute script %s/%s failed: %w", module, name, err)
		}
	}

	return nil
}
