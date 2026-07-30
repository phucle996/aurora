// ============================================================================
// 🗺️ ARCHITECTURAL COMPONENT: DATABASE MIGRATION ENGINE (HIERARCHY)
// ============================================================================
// CONTRACT: Thực hiện cài đặt và nâng cấp database schema cho phân hệ Hierarchy
// bằng cách chạy tuần tự các tệp migration SQL đã được nhúng trong ứng dụng.
//
// SOT: Các tệp SQL trong internal/hierarchy/migrations là nguồn dữ liệu duy
// nhất quy định database schema của Hierarchy module.
//
// BOUNDARY: Hoạt động hoàn toàn độc lập và chỉ thay đổi dữ liệu cấu trúc bên trong
// database schema được thiết lập riêng cho Hierarchy, không ảnh hưởng schema khác.
// ============================================================================

package hierarchy

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"controlplane/internal/config"
	hierarchyMigrations "controlplane/internal/hierarchy/migrations"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// hierarchySchemaIdentPattern kiểm tra tính hợp lệ của tên schema để ngăn SQL injection.
// Do PostgreSQL không cho phép tham số hóa (parameterized query) tên schema trong các câu lệnh DDL động,
// việc kiểm tra định dạng tên schema trước khi thực thi là bắt buộc.
var hierarchySchemaIdentPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ApplyMigrations áp dụng baseline migration của phân hệ Hierarchy.
func ApplyMigrations(ctx context.Context, conn *pgxpool.Conn, cfg *config.Config) error {
	// B1: Kiểm tra tính hợp lệ của connection và cấu hình.
	if conn == nil {
		return fmt.Errorf("hierarchy migration: connection is nil")
	}
	if cfg == nil {
		return fmt.Errorf("hierarchy migration: config is nil")
	}
	schema := strings.TrimSpace(cfg.SchemaSQL.Hierarchy)

	// B2: App bootstrap owns the transaction and advisory lock. Keeping this
	// function transaction-free avoids nested BEGIN/COMMIT and lets a failure
	// roll back every module migration atomically.
	// B3: Đảm bảo database schema đích được khởi tạo và tồn tại.
	if err := ensureMigrationSchema(ctx, conn, schema); err != nil {
		return err
	}

	// B4: Thiết lập search_path của session connection về database schema đích.
	if err := setMigrationSearchPath(ctx, conn, schema+",public"); err != nil {
		return err
	}

	// B5: Đọc và thực thi tuần tự các file migration được nhúng trong thư mục.
	if err := applyEmbeddedMigrations(ctx, conn, "hierarchy", hierarchyMigrations.Files); err != nil {
		return err
	}

	return nil
}

// ensureMigrationSchema đảm bảo rằng database schema đích đã tồn tại trong database.
func ensureMigrationSchema(ctx context.Context, conn *pgxpool.Conn, schema string) error {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		return fmt.Errorf("hierarchy migration: schema is required")
	}
	// Ngăn ngừa SQL Injection trước khi đưa schema name vào câu lệnh CREATE SCHEMA.
	if !hierarchySchemaIdentPattern.MatchString(schema) {
		return fmt.Errorf("hierarchy migration: invalid schema name %q", schema)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema)); err != nil {
		return fmt.Errorf("hierarchy migration: create schema %s: %w", schema, err)
	}
	return nil
}

// setMigrationSearchPath thiết lập search_path cho connection hiện tại.
func setMigrationSearchPath(ctx context.Context, conn *pgxpool.Conn, searchPath string) error {
	searchPath = strings.TrimSpace(searchPath)
	if searchPath == "" {
		return fmt.Errorf("hierarchy migration: search_path is required")
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", searchPath)); err != nil {
		return fmt.Errorf("hierarchy migration: set search_path to %s: %w", searchPath, err)
	}
	return nil
}

// applyEmbeddedMigrations đọc thư mục embedded migrations và thực thi tuần tự các file SQL.
func applyEmbeddedMigrations(ctx context.Context, conn *pgxpool.Conn, module string, files fs.FS) error {
	// B1: Đọc thư mục chứa các tệp tin migration đã được nhúng.
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return fmt.Errorf("hierarchy migration: read %s embedded migrations: %w", module, err)
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
			return fmt.Errorf("hierarchy migration: read %s/%s: %w", module, name, err)
		}
		query := string(queryBytes)
		if strings.TrimSpace(query) == "" {
			continue
		}
		// Sử dụng simple protocol (QueryExecModeSimpleProtocol) vì pgx mặc định biên dịch trước (prepare) câu lệnh,
		// vốn không hỗ trợ thực thi nhiều câu lệnh SQL ngăn cách bởi dấu chấm phẩy trong cùng một truy vấn.
		if _, err := conn.Exec(ctx, query, pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("hierarchy migration: apply %s/%s: %w", module, name, err)
		}
	}

	return nil
}
