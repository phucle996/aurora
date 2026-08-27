package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
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
// App bootstrap sở hữu transaction và advisory lock dùng chung cho toàn bộ
// migration graph của Controlplane.
func ApplyMigrations(ctx context.Context, conn *pgxpool.Conn, cfg *config.Config) error {
	if conn == nil {
		return fmt.Errorf("storage migration: connection is nil")
	}
	if cfg == nil {
		return fmt.Errorf("storage migration: config is nil")
	}
	schema := strings.TrimSpace(cfg.SchemaSQL.Storage)

	// The app bootstrap owns the transaction and advisory lock. This module
	// only applies its embedded files inside that caller-owned transaction.

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
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", searchPath)); err != nil {
		return fmt.Errorf("storage migration: set search_path to %s: %w", searchPath, err)
	}
	return nil
}

func applyEmbeddedMigrations(ctx context.Context, conn *pgxpool.Conn, module string, files fs.FS) error {
	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS storage_schema_migrations (
			name text PRIMARY KEY,
			checksum bytea NOT NULL CHECK (octet_length(checksum)=32),
			applied_at timestamptz NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("storage migration: create migration ledger: %w", err)
	}

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
		checksum := sha256.Sum256(queryBytes)
		var appliedChecksum []byte
		err = conn.QueryRow(ctx, `SELECT checksum FROM storage_schema_migrations WHERE name=$1`, name).Scan(&appliedChecksum)
		switch {
		case err == nil:
			if !bytes.Equal(appliedChecksum, checksum[:]) {
				return fmt.Errorf("storage migration: checksum changed for applied baseline %s", name)
			}
			continue
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("storage migration: read ledger for %s: %w", name, err)
		}
		if _, err := conn.Exec(ctx, query, pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("storage migration: apply %s/%s: %w", module, name, err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO storage_schema_migrations (name, checksum) VALUES ($1, $2)`, name, checksum[:]); err != nil {
			return fmt.Errorf("storage migration: record %s/%s: %w", module, name, err)
		}
	}

	return nil
}
