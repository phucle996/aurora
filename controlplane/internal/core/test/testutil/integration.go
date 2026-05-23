package testutil

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"controlplane/internal/config"
	core "controlplane/internal/core"
	"controlplane/internal/security"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

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

func NewCoreTestConfig(schema string) *config.Config {
	cfg := config.LoadConfig()
	cfg.SchemaSQL.Core = schema
	cfg.App.NodeID = "core-test-node"
	cfg.Psql.Host = envString("CORE_TEST_PSQL_HOST", "127.0.0.1")
	cfg.Psql.Port = envInt("CORE_TEST_PSQL_PORT", 15433)
	cfg.Psql.User = envString("CORE_TEST_PSQL_USER", "postgres")
	cfg.Psql.Password = envString("CORE_TEST_PSQL_PASSWORD", "postgres")
	cfg.Psql.DBName = envString("CORE_TEST_PSQL_DBNAME", "controlplane")
	cfg.Psql.SSLMode = envString("CORE_TEST_PSQL_SSLMODE", "disable")
	cfg.Redis.Addr = envString("CORE_TEST_REDIS_ADDR", "127.0.0.1:16380")
	cfg.Redis.Password = envString("CORE_TEST_REDIS_PASSWORD", "")
	cfg.Redis.DB = envInt("CORE_TEST_REDIS_DB", 0)
	cfg.Security.SecretCacheTTL = time.Hour
	cfg.Security.RuntimeMasterKey = envString("CORE_TEST_RUNTIME_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	return cfg
}

func UniqueSchema(prefix string) string {
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UTC().UnixNano(), rand.Intn(10000))
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
	return client
}

func PrepareCoreSchema(t testing.TB, cfg *config.Config, db *pgxpool.Pool) {
	t.Helper()
	conn, err := db.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire db conn: %v", err)
	}
	defer conn.Release()
	if err := core.ApplyMigrations(context.Background(), conn, cfg); err != nil {
		t.Fatalf("apply core migrations: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.Exec(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", cfg.SchemaSQL.Core))
	})
}

func SetRuntimeMasterKeyFromConfig(t testing.TB, cfg *config.Config) {
	t.Helper()
	key, err := DecodeRuntimeMasterKey(cfg.Security.RuntimeMasterKey)
	if err != nil {
		t.Fatalf("decode runtime master key: %v", err)
	}
	security.SetRuntimeMasterKey(key)
	t.Cleanup(func() { security.SetRuntimeMasterKey(nil) })
}

func DecodeRuntimeMasterKey(encoded string) ([]byte, error) {
	return decodeRuntimeMasterKey(encoded)
}

func WaitUntil(t testing.TB, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not reached before timeout")
}
