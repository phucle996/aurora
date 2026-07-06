package testutil

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"controlplane/internal/config"
	mailmigrations "controlplane/internal/mail/migrations"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func NewMailTestConfig(schema string) *config.Config {
	cfg := config.LoadConfig()
	cfg.SchemaSQL.Mail = schema
	cfg.App.AppName = "mail-test-node"
	cfg.Psql.Host = envString("MAIL_TEST_PSQL_HOST", "127.0.0.1")
	cfg.Psql.Port = envInt("MAIL_TEST_PSQL_PORT", 15434)
	cfg.Psql.User = envString("MAIL_TEST_PSQL_USER", "postgres")
	cfg.Psql.Password = envString("MAIL_TEST_PSQL_PASSWORD", "postgres")
	cfg.Psql.DBName = envString("MAIL_TEST_PSQL_DBNAME", "controlplane")
	cfg.Psql.SSLMode = envString("MAIL_TEST_PSQL_SSLMODE", "disable")
	return cfg
}

func envString(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func OpenPostgres(t testing.TB, cfg *config.Config) *pgxpool.Pool {
	t.Helper()
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s", cfg.Psql.Host, cfg.Psql.Port, cfg.Psql.User, cfg.Psql.Password, cfg.Psql.DBName, cfg.Psql.SSLMode)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

func PrepareMailSchema(t testing.TB, cfg *config.Config, db *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	conn, err := db.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire db connection for migration: %v", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transaction for migration: %v", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", cfg.SchemaSQL.Mail)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s,public", cfg.SchemaSQL.Mail)); err != nil {
		t.Fatalf("set search_path: %v", err)
	}

	entries, err := fs.ReadDir(mailmigrations.Files, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".up.sql") {
			names = append(names, name)
		}
	}
	for _, name := range names {
		queryBytes, err := fs.ReadFile(mailmigrations.Files, name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		query := strings.TrimSpace(string(queryBytes))
		if query == "" {
			continue
		}
		if _, err := tx.Exec(ctx, query, pgx.QueryExecModeSimpleProtocol); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit migration transaction: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.Exec(cleanupCtx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", cfg.SchemaSQL.Mail))
	})
}

func UniqueSchema(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UTC().UnixNano())
}
