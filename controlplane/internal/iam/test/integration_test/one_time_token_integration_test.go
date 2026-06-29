package integration_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	iamSvcImpl "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamtestutil "controlplane/internal/iam/test/testutil"

	"github.com/google/uuid"
)

func TestOneTimeTokenIntegrationConsumeOnce(t *testing.T) {
	schema := iamtestutil.UniqueSchema("iam_ott_once")
	cfg := iamtestutil.NewIAMTestConfig(schema)
	db := iamtestutil.OpenPostgres(t, cfg)
	iamtestutil.PrepareIAMSchema(t, cfg, db)
	rdb := iamtestutil.OpenRedis(t, cfg)

	svc := iamSvcImpl.NewOneTimeTokenService(cfg, makeIntegrationRegistry(rdb))

	userID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("failed to generate userID: %v", err)
	}

	token, _, err := svc.Issue(context.Background(), "account_verify", userID)
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}

	ok, err := svc.Consume(context.Background(), "account_verify", userID, token)
	if err != nil || !ok {
		t.Fatalf("first consume expected success, got ok=%v err=%v", ok, err)
	}

	ok, err = svc.Consume(context.Background(), "account_verify", userID, token)
	if !errors.Is(err, iamTaxonomy.ErrTokenExpired) {
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

	svc := iamSvcImpl.NewOneTimeTokenService(cfg, makeIntegrationRegistry(rdb))

	userID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("failed to generate userID: %v", err)
	}

	oldToken, _, err := svc.Issue(context.Background(), "account_verify", userID)
	if err != nil {
		t.Fatalf("issue old token failed: %v", err)
	}
	newToken, _, err := svc.Issue(context.Background(), "account_verify", userID)
	if err != nil {
		t.Fatalf("issue new token failed: %v", err)
	}

	ok, err := svc.Consume(context.Background(), "account_verify", userID, oldToken)
	if !errors.Is(err, iamTaxonomy.ErrTokenExpired) {
		t.Fatalf("old token must be invalid after overwrite, got %v", err)
	}
	if ok {
		t.Fatal("old token consume must be false")
	}

	ok, err = svc.Consume(context.Background(), "account_verify", userID, newToken)
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

	svc := iamSvcImpl.NewOneTimeTokenService(cfg, makeIntegrationRegistry(rdb))

	userID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("failed to generate userID: %v", err)
	}

	token, _, err := svc.Issue(context.Background(), "account_verify", userID)
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	time.Sleep(1300 * time.Millisecond)

	ok, err := svc.Consume(context.Background(), "account_verify", userID, token)
	if !errors.Is(err, iamTaxonomy.ErrTokenExpired) {
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
