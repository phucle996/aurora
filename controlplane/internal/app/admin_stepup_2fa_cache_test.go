package app

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func TestLoadAdminStepUp2FASettingsCachesSourceInRedis(t *testing.T) {
	redisServer := miniredis.RunT(t)
	rds := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = rds.Close() })

	calls := 0
	updatedAt := time.Now().UTC()
	loadFromSource := func(context.Context) (string, time.Time, error) {
		calls++
		return "cipher-from-db", updatedAt, nil
	}

	firstCiphertext, firstUpdatedAt, err := loadAdminStepUp2FASettings(context.Background(), rds, loadFromSource)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if firstCiphertext != "cipher-from-db" || !firstUpdatedAt.Equal(updatedAt) {
		t.Fatalf("first load = (%q, %s), want (%q, %s)", firstCiphertext, firstUpdatedAt, "cipher-from-db", updatedAt)
	}

	secondCiphertext, secondUpdatedAt, err := loadAdminStepUp2FASettings(context.Background(), rds, loadFromSource)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if secondCiphertext != "cipher-from-db" || !secondUpdatedAt.Equal(updatedAt) {
		t.Fatalf("second load = (%q, %s), want cached (%q, %s)", secondCiphertext, secondUpdatedAt, "cipher-from-db", updatedAt)
	}
	if calls != 1 {
		t.Fatalf("source calls = %d, want 1", calls)
	}
}

func TestLoadAdminStepUp2FASettingsFallsBackWhenRedisUnavailable(t *testing.T) {
	rds := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:0", DialTimeout: 10 * time.Millisecond})
	t.Cleanup(func() { _ = rds.Close() })

	updatedAt := time.Now().UTC()
	ciphertext, gotUpdatedAt, err := loadAdminStepUp2FASettings(context.Background(), rds, func(context.Context) (string, time.Time, error) {
		return "cipher-from-db", updatedAt, nil
	})
	if err != nil {
		t.Fatalf("load with broken redis should fallback: %v", err)
	}
	if ciphertext != "cipher-from-db" || !gotUpdatedAt.Equal(updatedAt) {
		t.Fatalf("load = (%q, %s), want (%q, %s)", ciphertext, gotUpdatedAt, "cipher-from-db", updatedAt)
	}
}
