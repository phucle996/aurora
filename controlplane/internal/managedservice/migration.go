package managedservice

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"controlplane/internal/config"
	managedservicemigrations "controlplane/internal/managedservice/migrations"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var managedServiceSchemaIdentPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ApplyMigrations installs only durable Controlplane desired state. The app owns
// the transaction and advisory lock, so a partial Managed Service baseline can
// never become visible beside the rest of the Controlplane migration graph.
func ApplyMigrations(ctx context.Context, conn *pgxpool.Conn, cfg *config.Config) error {
	if conn == nil {
		return fmt.Errorf("managed service migration: connection is nil")
	}
	if cfg == nil {
		return fmt.Errorf("managed service migration: config is nil")
	}
	schema := strings.TrimSpace(cfg.SchemaSQL.ManagedService)
	if schema == "" {
		return fmt.Errorf("managed service migration: schema is required")
	}
	if !managedServiceSchemaIdentPattern.MatchString(schema) {
		return fmt.Errorf("managed service migration: invalid schema name %q", schema)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema)); err != nil {
		return fmt.Errorf("managed service migration: create schema %s: %w", schema, err)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s,public", schema)); err != nil {
		return fmt.Errorf("managed service migration: set search_path: %w", err)
	}

	entries, err := fs.ReadDir(managedservicemigrations.Files, ".")
	if err != nil {
		return fmt.Errorf("managed service migration: read embedded migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".up.sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		query, err := fs.ReadFile(managedservicemigrations.Files, name)
		if err != nil {
			return fmt.Errorf("managed service migration: read %s: %w", name, err)
		}
		if strings.TrimSpace(string(query)) == "" {
			continue
		}
		if _, err := conn.Exec(ctx, string(query), pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("managed service migration: apply %s: %w", name, err)
		}
	}
	return nil
}
