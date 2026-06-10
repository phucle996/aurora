package svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	iamSvcImpl "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/pkg/apperr"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
)

func setupTestRegistry(t *testing.T) *cacheengine.CacheRegistry {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	l1 := cacheengine.NewL1Cache()
	reg := cacheengine.NewCacheRegistry(l1)
	reg.L2 = cacheengine.NewL2Cache(rdb)
	reg.Exec = cacheengine.NewL2LuaExecutor(rdb)
	return reg
}

func TestOneTimeTokenServiceIssueAndConsumeSuccess(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Security.OneTimeTokenTTL = 10 * time.Minute

	userID := uuid.Must(uuid.NewV7())
	registry := setupTestRegistry(t)

	svc := iamSvcImpl.NewOneTimeTokenService(cfg, registry)
	token, expiresAt, err := svc.Issue(context.Background(), "account_verify", userID)
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	if token == "" {
		t.Fatal("expected plaintext token")
	}
	if expiresAt.IsZero() {
		t.Fatal("expected non-zero expiresAt")
	}

	consumed, err := svc.Consume(context.Background(), "account_verify", userID, token)
	if err != nil {
		t.Fatalf("consume failed: %v", err)
	}
	if !consumed {
		t.Fatal("expected consumed=true")
	}
}

func TestOneTimeTokenServiceInvalidPurposeOrUser(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Security.OneTimeTokenTTL = time.Minute
	registry := setupTestRegistry(t)
	svc := iamSvcImpl.NewOneTimeTokenService(cfg, registry)

	userID := uuid.Must(uuid.NewV7())
	_, _, err := svc.Issue(context.Background(), "", userID)
	if !errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}

	_, err = svc.Consume(context.Background(), "account_verify", uuid.Nil, "abc")
	if !errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
}

func TestOneTimeTokenServiceInvalidTTL(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Security.OneTimeTokenTTL = 0
	registry := setupTestRegistry(t)
	svc := iamSvcImpl.NewOneTimeTokenService(cfg, registry)

	userID := uuid.Must(uuid.NewV7())
	_, _, err := svc.Issue(context.Background(), "account_verify", userID)
	if !errors.Is(err, iamTaxonomy.ErrTokenIssueFailed) {
		t.Fatalf("expected ErrTokenIssueFailed, got %v", err)
	}
}

func TestOneTimeTokenServiceConsumeTwice(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Security.OneTimeTokenTTL = time.Minute
	registry := setupTestRegistry(t)
	svc := iamSvcImpl.NewOneTimeTokenService(cfg, registry)

	userID := uuid.Must(uuid.NewV7())
	token, _, err := svc.Issue(context.Background(), "account_verify", userID)
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}

	ok, err := svc.Consume(context.Background(), "account_verify", userID, token)
	if err != nil || !ok {
		t.Fatalf("first consume expected success, got ok=%v err=%v", ok, err)
	}

	ok, err = svc.Consume(context.Background(), "account_verify", userID, token)
	if !errors.Is(err, iamTaxonomy.ErrTokenRefreshExpired) {
		t.Fatalf("expected ErrTokenRefreshExpired, got %v", err)
	}
	if ok {
		t.Fatal("second consume must be false")
	}
}

func TestOneTimeTokenServiceCacheError(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Security.OneTimeTokenTTL = time.Minute

	userID := uuid.Must(uuid.NewV7())
	registry := setupTestRegistry(t)
	registry.L2.Client().Close()

	svc := iamSvcImpl.NewOneTimeTokenService(cfg, registry)

	_, _, err := svc.Issue(context.Background(), "account_verify", userID)
	if !errors.Is(err, iamTaxonomy.ErrTokenIssueFailed) {
		t.Fatalf("expected ErrTokenIssueFailed, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Outcome != "cache_unavailable" {
		t.Fatalf("unexpected outcome: %q", appErr.Outcome)
	}
}
