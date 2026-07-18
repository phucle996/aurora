package app

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	billingmigrations "cost-manager/api/migrations"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// billingSchemaIdentPattern bảo vệ schema name khỏi SQL injection
var billingSchemaIdentPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// runBillingMigrations chạy toàn bộ embedded SQL migration cho billing schema.
// Sử dụng advisory lock để tránh xung đột giữa nhiều instance HA khởi động đồng thời.
func runBillingMigrations(ctx context.Context, db *pgxpool.Pool) error {
	// Acquire một connection riêng để giữ advisory lock suốt quá trình migration
	conn, err := db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("billing migration: acquire connection: %w", err)
	}
	defer conn.Release()

	// Mở transaction bao toàn bộ quá trình migrate — rollback khi có lỗi
	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		return fmt.Errorf("billing migration: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.Exec(context.Background(), "ROLLBACK")
		}
	}()

	// Advisory xact lock (key: 20260001 = billing) để serialise concurrent HA nodes
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_xact_lock(20260001)"); err != nil {
		return fmt.Errorf("billing migration: acquire advisory lock: %w", err)
	}

	// Đảm bảo schema billing tồn tại
	if err := ensureBillingSchema(ctx, conn, "billing"); err != nil {
		return err
	}

	// Set search_path để các câu SQL không cần prefix schema
	if err := setBillingSearchPath(ctx, conn, "billing,public"); err != nil {
		return err
	}

	// Thực thi từng file *.up.sql theo thứ tự tên file
	if err := applyBillingMigrations(ctx, conn, billingmigrations.Files); err != nil {
		return err
	}

	if _, err := conn.Exec(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("billing migration: commit tx: %w", err)
	}
	committed = true
	return nil
}

// ensureBillingSchema tạo schema nếu chưa tồn tại (idempotent).
func ensureBillingSchema(ctx context.Context, conn *pgxpool.Conn, schema string) error {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		return fmt.Errorf("billing migration: schema name is empty")
	}
	if !billingSchemaIdentPattern.MatchString(schema) {
		return fmt.Errorf("billing migration: invalid schema name %q", schema)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema)); err != nil {
		return fmt.Errorf("billing migration: create schema %s: %w", schema, err)
	}
	return nil
}

// setBillingSearchPath thiết lập search_path trong transaction hiện tại.
func setBillingSearchPath(ctx context.Context, conn *pgxpool.Conn, searchPath string) error {
	searchPath = strings.TrimSpace(searchPath)
	if searchPath == "" {
		return fmt.Errorf("billing migration: search_path is empty")
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %s", searchPath)); err != nil {
		return fmt.Errorf("billing migration: set search_path %s: %w", searchPath, err)
	}
	return nil
}

// applyBillingMigrations đọc và thực thi các file *.up.sql theo thứ tự alphabetical.
func applyBillingMigrations(ctx context.Context, conn *pgxpool.Conn, files fs.FS) error {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return fmt.Errorf("billing migration: read embedded migrations: %w", err)
	}

	// Lấy danh sách file *.up.sql và sắp xếp tên để đảm bảo thứ tự đúng
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

	// Thực thi từng file SQL — dùng SimpleProtocol để hỗ trợ multi-statement SQL
	for _, name := range names {
		queryBytes, err := fs.ReadFile(files, name)
		if err != nil {
			return fmt.Errorf("billing migration: read %s: %w", name, err)
		}
		query := string(queryBytes)
		if strings.TrimSpace(query) == "" {
			continue
		}
		if _, err := conn.Exec(ctx, query, pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("billing migration: apply %s: %w", name, err)
		}
	}

	return nil
}
