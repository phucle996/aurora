package iam

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
	iammigrations "controlplane/internal/iam/migrations"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var iamSchemaIdentPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func ApplyMigrations(ctx context.Context, conn *pgxpool.Conn, cfg *config.Config) error {
	if conn == nil {
		return fmt.Errorf("iam migration: connection is nil")
	}
	if cfg == nil {
		return fmt.Errorf("iam migration: config is nil")
	}
	schema := strings.TrimSpace(cfg.SchemaSQL.IAM)

	// The app bootstrap owns the transaction and advisory lock. This module
	// only applies its embedded files inside that caller-owned transaction.
	if err := ensureMigrationSchema(ctx, conn, schema); err != nil {
		return err
	}
	if err := setMigrationSearchPath(ctx, conn, schema+",public"); err != nil {
		return err
	}
	if err := applyEmbeddedMigrations(ctx, conn, "iam", iammigrations.Files); err != nil {
		return err
	}

	return nil
}

func ensureMigrationSchema(ctx context.Context, conn *pgxpool.Conn, schema string) error {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		return fmt.Errorf("iam migration: schema is required")
	}
	if !iamSchemaIdentPattern.MatchString(schema) {
		return fmt.Errorf("iam migration: invalid schema name %q", schema)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema)); err != nil {
		return fmt.Errorf("iam migration: create schema %s: %w", schema, err)
	}
	return nil
}

func setMigrationSearchPath(ctx context.Context, conn *pgxpool.Conn, searchPath string) error {
	searchPath = strings.TrimSpace(searchPath)
	if searchPath == "" {
		return fmt.Errorf("iam migration: search_path is required")
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", searchPath)); err != nil {
		return fmt.Errorf("iam migration: set search_path to %s: %w", searchPath, err)
	}
	return nil
}

func applyEmbeddedMigrations(ctx context.Context, conn *pgxpool.Conn, module string, files fs.FS) error {
	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS iam_schema_migrations (
			name text PRIMARY KEY,
			checksum bytea NOT NULL CHECK (octet_length(checksum)=32),
			applied_at timestamptz NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("iam migration: create migration ledger: %w", err)
	}

	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return fmt.Errorf("iam migration: read %s embedded migrations: %w", module, err)
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
			return fmt.Errorf("iam migration: read %s/%s: %w", module, name, err)
		}
		query := string(queryBytes)
		if strings.TrimSpace(query) == "" {
			continue
		}
		checksum := sha256.Sum256(queryBytes)
		var appliedChecksum []byte
		err = conn.QueryRow(ctx, `SELECT checksum FROM iam_schema_migrations WHERE name=$1`, name).Scan(&appliedChecksum)
		switch {
		case err == nil:
			// [COMMENT]: A filename is immutable once applied. Silent replay or
			// accepting edited SQL would make HA replicas observe different schema.
			if !bytes.Equal(appliedChecksum, checksum[:]) {
				return fmt.Errorf("iam migration: checksum changed for applied baseline %s", name)
			}
			continue
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("iam migration: read ledger for %s: %w", name, err)
		}
		if _, err := conn.Exec(ctx, query, pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("iam migration: apply %s/%s: %w", module, name, err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO iam_schema_migrations (name, checksum) VALUES ($1, $2)`, name, checksum[:]); err != nil {
			return fmt.Errorf("iam migration: record %s/%s: %w", module, name, err)
		}
	}

	return nil
}
