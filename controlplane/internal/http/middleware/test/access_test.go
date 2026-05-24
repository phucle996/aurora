package middleware_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"controlplane/internal/http/middleware"
	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func TestIsBlacklistedCachesRevokedJTI(t *testing.T) {
	redisServer := miniredis.RunT(t)
	rds := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = rds.Close() })

	jti := "jti-revoked-" + strings.ReplaceAll(t.Name(), "/", "-")
	if err := rds.Set(context.Background(), "iam:blacklist:"+jti, "1", time.Minute).Err(); err != nil {
		t.Fatalf("set blacklist key: %v", err)
	}

	blacklisted, err := middleware.IsBlacklisted(context.Background(), rds, jti)
	if err != nil {
		t.Fatalf("first blacklist check: %v", err)
	}
	if !blacklisted {
		t.Fatal("expected revoked token")
	}

	if err := rds.Del(context.Background(), "iam:blacklist:"+jti).Err(); err != nil {
		t.Fatalf("delete blacklist key: %v", err)
	}
	blacklisted, err = middleware.IsBlacklisted(context.Background(), rds, jti)
	if err != nil {
		t.Fatalf("cached blacklist check: %v", err)
	}
	if !blacklisted {
		t.Fatal("expected positive revoked cache hit")
	}
}
