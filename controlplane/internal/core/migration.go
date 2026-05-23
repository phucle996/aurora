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

var coreSchemaIdentPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func ApplyMigrations(ctx context.Context, conn *pgxpool.Conn, cfg *config.Config) error {
	if conn == nil {
		return fmt.Errorf("core migration: connection is nil")
	}
	if cfg == nil {
		return fmt.Errorf("core migration: config is nil")
	}
	schema := strings.TrimSpace(cfg.SchemaSQL.Core)
	if err := ensureMigrationSchema(ctx, conn, schema); err != nil {
		return err
	}
	if err := setMigrationSearchPath(ctx, conn, schema+",public"); err != nil {
		return err
	}
	if err := applyEmbeddedMigrations(ctx, conn, "core", coremigrations.Files); err != nil {
		return err
	}
	return nil
}

func ensureMigrationSchema(ctx context.Context, conn *pgxpool.Conn, schema string) error {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		return fmt.Errorf("core migration: schema is required")
	}
	if !coreSchemaIdentPattern.MatchString(schema) {
		return fmt.Errorf("core migration: invalid schema name %q", schema)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema)); err != nil {
		return fmt.Errorf("core migration: create schema %s: %w", schema, err)
	}
	return nil
}

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

func applyEmbeddedMigrations(ctx context.Context, conn *pgxpool.Conn, module string, files fs.FS) error {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return fmt.Errorf("core migration: read %s embedded migrations: %w", module, err)
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
			return fmt.Errorf("core migration: read %s/%s: %w", module, name, err)
		}
		query := string(queryBytes)
		if strings.TrimSpace(query) == "" {
			continue
		}
		if _, err := conn.Exec(ctx, query, pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("core migration: apply %s/%s: %w", module, name, err)
		}
	}

	return nil
}
