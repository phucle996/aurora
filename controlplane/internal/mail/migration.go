package mail

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"controlplane/internal/config"
	mailmigrations "controlplane/internal/mail/migrations"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var mailSchemaIdentPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func ApplyMigrations(ctx context.Context, conn *pgxpool.Conn, cfg *config.Config) error {
	if conn == nil {
		return fmt.Errorf("mail migration: connection is nil")
	}
	if cfg == nil {
		return fmt.Errorf("mail migration: config is nil")
	}

	schema := strings.TrimSpace(cfg.SchemaSQL.Mail)

	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		return fmt.Errorf("mail migration: begin tx: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(ctx, "ROLLBACK")
	}()

	if err := ensureMigrationSchema(ctx, conn, schema); err != nil {
		return err
	}
	if err := setMigrationSearchPath(ctx, conn, schema+",public"); err != nil {
		return err
	}
	if err := applyEmbeddedMigrations(ctx, conn, schema, mailmigrations.Files); err != nil {
		return err
	}

	if _, err := conn.Exec(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("mail migration: commit tx: %w", err)
	}
	return nil
}

func ensureMigrationSchema(ctx context.Context, conn *pgxpool.Conn, schema string) error {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		return fmt.Errorf("mail migration: schema is required")
	}
	if !mailSchemaIdentPattern.MatchString(schema) {
		return fmt.Errorf("mail migration: invalid schema name %q", schema)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema)); err != nil {
		return fmt.Errorf("mail migration: create schema %s: %w", schema, err)
	}
	return nil
}

func setMigrationSearchPath(ctx context.Context, conn *pgxpool.Conn, searchPath string) error {
	searchPath = strings.TrimSpace(searchPath)
	if searchPath == "" {
		return fmt.Errorf("mail migration: search_path is required")
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %s", searchPath)); err != nil {
		return fmt.Errorf("mail migration: set search_path to %s: %w", searchPath, err)
	}
	return nil
}

func applyEmbeddedMigrations(ctx context.Context, conn *pgxpool.Conn, module string, files fs.FS) error {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return fmt.Errorf("mail migration: read %s embedded migrations: %w", module, err)
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
			return fmt.Errorf("mail migration: read %s/%s: %w", module, name, err)
		}
		query := string(queryBytes)
		if strings.TrimSpace(query) == "" {
			continue
		}
		if _, err := conn.Exec(ctx, query, pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("mail migration: apply %s/%s: %w", module, name, err)
		}
	}

	return nil
}
