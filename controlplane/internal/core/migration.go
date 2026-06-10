// ============================================================================
// 🗺️ ARCHITECTURAL COMPONENT: DATABASE MIGRATION ENGINE (CORE)
// ============================================================================
// CONTRACT: Thực hiện cài đặt và nâng cấp cấu trúc database schema cho phân hệ Core
// bằng cách chạy tuần tự các tệp migration SQL đã được nhúng trong ứng dụng.
//
// SOT: Các tệp tin SQL trong package coremigrations (thư mục internal/core/migrations/)
// là nguồn dữ liệu duy nhất quy định database schema của Core module.
//
// BOUNDARY: Hoạt động hoàn toàn độc lập và chỉ thay đổi dữ liệu cấu trúc bên trong
// database schema được thiết lập riêng cho Core, không ảnh hưởng đến các schema khác.
// ============================================================================

package core

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"controlplane/internal/config"
	coremigrations "controlplane/internal/core/migrations"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// coreSchemaIdentPattern kiểm tra tính hợp lệ của tên schema để ngăn ngừa lỗ hổng SQL Injection.
// Do PostgreSQL không cho phép tham số hóa (parameterized query) tên schema trong các câu lệnh DDL động,
// việc kiểm tra định dạng tên schema trước khi thực thi là bắt buộc.
var coreSchemaIdentPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ApplyMigrations thực hiện áp dụng các migration lên database của phân hệ Core.
func ApplyMigrations(ctx context.Context, conn *pgxpool.Conn, cfg *config.Config) error {
	// B1: Kiểm tra tính hợp lệ của connection và cấu hình.
	if conn == nil {
		return fmt.Errorf("core migration: connection is nil")
	}
	if cfg == nil {
		return fmt.Errorf("core migration: config is nil")
	}
	schema := strings.TrimSpace(cfg.SchemaSQL.Core)

	// B2: Khởi tạo database transaction và đăng ký trì hoãn rollback khi có lỗi phát sinh.
	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		return fmt.Errorf("core migration: begin tx: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(ctx, "ROLLBACK")
	}()

	// Acquire a transaction-level advisory lock to serialize concurrent migrations across HA nodes
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_xact_lock(1102)"); err != nil {
		return fmt.Errorf("core migration: acquire advisory lock: %w", err)
	}

	// B3: Đảm bảo database schema đích được khởi tạo và tồn tại.
	if err := ensureMigrationSchema(ctx, conn, schema); err != nil {
		return err
	}

	// B4: Thiết lập search_path của session connection về database schema đích.
	if err := setMigrationSearchPath(ctx, conn, schema+",public"); err != nil {
		return err
	}

	// B5: Đọc và thực thi tuần tự các file migration được nhúng trong thư mục.
	if err := applyEmbeddedMigrations(ctx, conn, "core", coremigrations.Files); err != nil {
		return err
	}

	// B6: Commit transaction để lưu trữ các thay đổi cấu trúc database atomically.
	if _, err := conn.Exec(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("core migration: commit tx: %w", err)
	}
	return nil
}

// ensureMigrationSchema đảm bảo rằng database schema đích đã tồn tại trong database.
func ensureMigrationSchema(ctx context.Context, conn *pgxpool.Conn, schema string) error {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		return fmt.Errorf("core migration: schema is required")
	}
	// Ngăn ngừa SQL Injection trước khi đưa schema name vào câu lệnh CREATE SCHEMA.
	if !coreSchemaIdentPattern.MatchString(schema) {
		return fmt.Errorf("core migration: invalid schema name %q", schema)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema)); err != nil {
		return fmt.Errorf("core migration: create schema %s: %w", schema, err)
	}
	return nil
}

// setMigrationSearchPath thiết lập search_path cho connection hiện tại.
func setMigrationSearchPath(ctx context.Context, conn *pgxpool.Conn, searchPath string) error {
	searchPath = strings.TrimSpace(searchPath)
	if searchPath == "" {
		return fmt.Errorf("core migration: search_path is required")
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %s", searchPath)); err != nil {
		return fmt.Errorf("core migration: set search_path to %s: %w", searchPath, err)
	}
	return nil
}

// applyEmbeddedMigrations đọc thư mục embedded migrations và thực thi tuần tự các file SQL.
func applyEmbeddedMigrations(ctx context.Context, conn *pgxpool.Conn, module string, files fs.FS) error {
	// B1: Đọc thư mục chứa các tệp tin migration đã được nhúng.
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return fmt.Errorf("core migration: read %s embedded migrations: %w", module, err)
	}

	// B2: Lọc các tệp tin có hậu tố .up.sql để tìm các truy vấn nâng cấp cấu trúc.
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

	// B3: Sắp xếp danh sách tên tệp tin theo thứ tự tăng dần đảm bảo thứ tự thực thi tuần tự của migration.
	sort.Strings(names)

	// B4: Đọc nội dung SQL của từng tệp tin và thực thi chúng bằng simple protocol của pgx.
	for _, name := range names {
		queryBytes, err := fs.ReadFile(files, name)
		if err != nil {
			return fmt.Errorf("core migration: read %s/%s: %w", module, name, err)
		}
		query := string(queryBytes)
		if strings.TrimSpace(query) == "" {
			continue
		}
		// Sử dụng simple protocol (QueryExecModeSimpleProtocol) vì pgx mặc định biên dịch trước (prepare) câu lệnh,
		// vốn không hỗ trợ thực thi nhiều câu lệnh SQL ngăn cách bởi dấu chấm phẩy trong cùng một truy vấn.
		if _, err := conn.Exec(ctx, query, pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("core migration: apply %s/%s: %w", module, name, err)
		}
	}

	return nil
}
