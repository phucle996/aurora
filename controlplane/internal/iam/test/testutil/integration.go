package testutil

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"controlplane/internal/config"
	coretestutil "controlplane/internal/core/test/testutil"
	iammigrations "controlplane/internal/iam/migrations"
	"controlplane/internal/security"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

const (
	RegisterUsernameBitmapKey = "iam:register:bitmap:username"
	RegisterEmailBitmapKey    = "iam:register:bitmap:email"
)

func NewIAMTestConfig(schema string) *config.Config {
	cfg := config.LoadConfig()
	cfg.SchemaSQL.IAM = schema
	cfg.App.AppName = "iam-test-node"
	cfg.Psql.Host = envString("IAM_TEST_PSQL_HOST", "127.0.0.1")
	cfg.Psql.Port = envInt("IAM_TEST_PSQL_PORT", 15433)
	cfg.Psql.User = envString("IAM_TEST_PSQL_USER", "postgres")
	cfg.Psql.Password = envString("IAM_TEST_PSQL_PASSWORD", "postgres")
	cfg.Psql.DBName = envString("IAM_TEST_PSQL_DBNAME", "controlplane")
	cfg.Psql.SSLMode = envString("IAM_TEST_PSQL_SSLMODE", "disable")
	cfg.Redis.Addr = envString("IAM_TEST_REDIS_ADDR", "127.0.0.1:16380")
	cfg.Redis.Password = envString("IAM_TEST_REDIS_PASSWORD", "")
	cfg.Redis.DB = envInt("IAM_TEST_REDIS_DB", 0)
	cfg.Security.RuntimeMasterKey = envString("IAM_TEST_RUNTIME_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
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

func OpenRedis(t testing.TB, cfg *config.Config) *goredis.Client {
	t.Helper()
	client := goredis.NewClient(&goredis.Options{Addr: cfg.Redis.Addr, Password: cfg.Redis.Password, DB: cfg.Redis.DB})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping redis: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Del(cleanupCtx, RegisterUsernameBitmapKey, RegisterEmailBitmapKey).Err()
	})
	return client
}

func PrepareIAMSchema(t testing.TB, cfg *config.Config, db *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", cfg.SchemaSQL.IAM)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %s,public", cfg.SchemaSQL.IAM)); err != nil {
		t.Fatalf("set search_path: %v", err)
	}

	entries, err := fs.ReadDir(iammigrations.Files, ".")
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
	sort.Strings(names)
	for _, name := range names {
		queryBytes, err := fs.ReadFile(iammigrations.Files, name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		query := strings.TrimSpace(string(queryBytes))
		if query == "" {
			continue
		}
		if _, err := conn.Exec(ctx, query, pgx.QueryExecModeSimpleProtocol); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.Exec(cleanupCtx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", cfg.SchemaSQL.IAM))
	})
}

func SetRuntimeMasterKeyFromConfig(t testing.TB, cfg *config.Config) {
	t.Helper()
	key, err := coretestutil.DecodeRuntimeMasterKey(cfg.Security.RuntimeMasterKey)
	if err != nil {
		t.Fatalf("decode runtime master key: %v", err)
	}
	security.SetRuntimeMasterKey(key)
	t.Cleanup(func() { security.SetRuntimeMasterKey(nil) })
}

func UniqueSchema(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UTC().UnixNano())
}

func UniqueIdentity(prefix string) (string, string) {
	username := fmt.Sprintf("%s_%d", prefix, time.Now().UTC().UnixNano())
	return username, username + "@example.com"
}

func CountUsersByIdentity(ctx context.Context, t testing.TB, db *pgxpool.Pool, schema string, username string, email string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s.users WHERE username = $1 AND email = $2", schema), username, email).Scan(&count); err != nil {
		t.Fatalf("count user row: %v", err)
	}
	return count
}
