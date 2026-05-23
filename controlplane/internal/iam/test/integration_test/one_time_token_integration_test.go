package integration_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"controlplane/internal/iam/cache"
	iamErrorx "controlplane/internal/iam/errorx"
	iamSvcImpl "controlplane/internal/iam/service"
	iamtestutil "controlplane/internal/iam/test/testutil"
)

func TestOneTimeTokenIntegrationConsumeOnce(t *testing.T) {
	schema := iamtestutil.UniqueSchema("iam_ott_once")
	cfg := iamtestutil.NewIAMTestConfig(schema)
	db := iamtestutil.OpenPostgres(t, cfg)
	iamtestutil.PrepareIAMSchema(t, cfg, db)
	rdb := iamtestutil.OpenRedis(t, cfg)

	svc := iamSvcImpl.NewOneTimeTokenService(cfg, iamCache.NewOneTimeTokenCache(rdb))

	token, _, err := svc.Issue(context.Background(), "account_verify", "u100")
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}

	ok, err := svc.Consume(context.Background(), "account_verify", "u100", token)
	if err != nil || !ok {
		t.Fatalf("first consume expected success, got ok=%v err=%v", ok, err)
	}

	ok, err = svc.Consume(context.Background(), "account_verify", "u100", token)
	if !errors.Is(err, iamErrorx.ErrOneTimeTokenInvalidOrExpired) {
		t.Fatalf("expected ErrOneTimeTokenInvalidOrExpired, got %v", err)
	}
	if ok {
		t.Fatal("second consume must be false")
	}
}

func TestOneTimeTokenIntegrationOverwriteOldToken(t *testing.T) {
	schema := iamtestutil.UniqueSchema("iam_ott_overwrite")
	cfg := iamtestutil.NewIAMTestConfig(schema)
	db := iamtestutil.OpenPostgres(t, cfg)
	iamtestutil.PrepareIAMSchema(t, cfg, db)
	rdb := iamtestutil.OpenRedis(t, cfg)

	svc := iamSvcImpl.NewOneTimeTokenService(cfg, iamCache.NewOneTimeTokenCache(rdb))

	oldToken, _, err := svc.Issue(context.Background(), "account_verify", "u200")
	if err != nil {
		t.Fatalf("issue old token failed: %v", err)
	}
	newToken, _, err := svc.Issue(context.Background(), "account_verify", "u200")
	if err != nil {
		t.Fatalf("issue new token failed: %v", err)
	}

	ok, err := svc.Consume(context.Background(), "account_verify", "u200", oldToken)
	if !errors.Is(err, iamErrorx.ErrOneTimeTokenInvalidOrExpired) {
		t.Fatalf("old token must be invalid after overwrite, got %v", err)
	}
	if ok {
		t.Fatal("old token consume must be false")
	}

	ok, err = svc.Consume(context.Background(), "account_verify", "u200", newToken)
	if err != nil || !ok {
		t.Fatalf("new token consume expected success, got ok=%v err=%v", ok, err)
	}
}

func TestOneTimeTokenIntegrationTTLExpire(t *testing.T) {
	schema := iamtestutil.UniqueSchema("iam_ott_ttl")
	cfg := iamtestutil.NewIAMTestConfig(schema)
	cfg.Security.OneTimeTokenTTL = 1 * time.Second
	db := iamtestutil.OpenPostgres(t, cfg)
	iamtestutil.PrepareIAMSchema(t, cfg, db)
	rdb := iamtestutil.OpenRedis(t, cfg)

	svc := iamSvcImpl.NewOneTimeTokenService(cfg, iamCache.NewOneTimeTokenCache(rdb))

	token, _, err := svc.Issue(context.Background(), "account_verify", "u300")
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	time.Sleep(1300 * time.Millisecond)

	ok, err := svc.Consume(context.Background(), "account_verify", "u300", token)
	if !errors.Is(err, iamErrorx.ErrOneTimeTokenInvalidOrExpired) {
		t.Fatalf("expected ErrOneTimeTokenInvalidOrExpired after ttl, got %v", err)
	}
	if ok {
		t.Fatal("consume must be false after ttl expire")
	}

	key := fmt.Sprintf("iam:ott:%s:%s", "account_verify", "u300")
	if ttl := rdb.TTL(context.Background(), key).Val(); ttl > 0 {
		t.Fatalf("expected key expired, ttl=%v", ttl)
	}
}
