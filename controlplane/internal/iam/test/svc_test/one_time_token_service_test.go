package svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"controlplane/internal/config"
	"controlplane/internal/iam/cache"
	iamErrorx "controlplane/internal/iam/errorx"
	iamSvcImpl "controlplane/internal/iam/service"
	"controlplane/pkg/apperr"
)

type oneTimeTokenCacheMock struct {
	setFn     func(ctx context.Context, purpose string, userID string, tokenHash string, ttl time.Duration) error
	consumeFn func(ctx context.Context, purpose string, userID string, tokenHash string) (bool, error)
}

func (m *oneTimeTokenCacheMock) SetHashedToken(ctx context.Context, purpose string, userID string, tokenHash string, ttl time.Duration) error {
	if m.setFn != nil {
		return m.setFn(ctx, purpose, userID, tokenHash, ttl)
	}
	return nil
}

func (m *oneTimeTokenCacheMock) ConsumeHashedToken(ctx context.Context, purpose string, userID string, tokenHash string) (bool, error) {
	if m.consumeFn != nil {
		return m.consumeFn(ctx, purpose, userID, tokenHash)
	}
	return false, nil
}

func TestOneTimeTokenServiceIssueAndConsumeSuccess(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Security.OneTimeTokenTTL = 10 * time.Minute

	var storedHash string
	cacheMock := &oneTimeTokenCacheMock{
		setFn: func(ctx context.Context, purpose string, userID string, tokenHash string, ttl time.Duration) error {
			if purpose != "account_verify" || userID != "u1" {
				t.Fatalf("unexpected purpose/user: %s/%s", purpose, userID)
			}
			if ttl != cfg.Security.OneTimeTokenTTL {
				t.Fatalf("unexpected ttl: %v", ttl)
			}
			storedHash = tokenHash
			return nil
		},
		consumeFn: func(ctx context.Context, purpose string, userID string, tokenHash string) (bool, error) {
			return tokenHash == storedHash, nil
		},
	}

	svc := iamSvcImpl.NewOneTimeTokenService(cfg, cacheMock)
	token, expiresAt, err := svc.Issue(context.Background(), "account_verify", "u1")
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	if token == "" {
		t.Fatal("expected plaintext token")
	}
	if expiresAt.IsZero() {
		t.Fatal("expected non-zero expiresAt")
	}

	consumed, err := svc.Consume(context.Background(), "account_verify", "u1", token)
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
	svc := iamSvcImpl.NewOneTimeTokenService(cfg, &oneTimeTokenCacheMock{})

	_, _, err := svc.Issue(context.Background(), "", "u1")
	if !errors.Is(err, iamErrorx.ErrOneTimeTokenInvalidPurposeOrUser) {
		t.Fatalf("expected ErrOneTimeTokenInvalidPurposeOrUser, got %v", err)
	}

	_, err = svc.Consume(context.Background(), "account_verify", "", "abc")
	if !errors.Is(err, iamErrorx.ErrOneTimeTokenInvalidPurposeOrUser) {
		t.Fatalf("expected ErrOneTimeTokenInvalidPurposeOrUser, got %v", err)
	}
}

func TestOneTimeTokenServiceInvalidTTL(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Security.OneTimeTokenTTL = 0
	svc := iamSvcImpl.NewOneTimeTokenService(cfg, &oneTimeTokenCacheMock{})

	_, _, err := svc.Issue(context.Background(), "account_verify", "u1")
	if !errors.Is(err, iamErrorx.ErrOneTimeTokenIssueFailed) {
		t.Fatalf("expected ErrOneTimeTokenIssueFailed, got %v", err)
	}
}

func TestOneTimeTokenServiceConsumeTwice(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Security.OneTimeTokenTTL = time.Minute

	used := false
	var storedHash string
	cacheMock := &oneTimeTokenCacheMock{
		setFn: func(ctx context.Context, purpose string, userID string, tokenHash string, ttl time.Duration) error {
			storedHash = tokenHash
			return nil
		},
		consumeFn: func(ctx context.Context, purpose string, userID string, tokenHash string) (bool, error) {
			if used {
				return false, nil
			}
			if tokenHash == storedHash {
				used = true
				return true, nil
			}
			return false, nil
		},
	}

	svc := iamSvcImpl.NewOneTimeTokenService(cfg, cacheMock)
	token, _, err := svc.Issue(context.Background(), "account_verify", "u1")
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}

	ok, err := svc.Consume(context.Background(), "account_verify", "u1", token)
	if err != nil || !ok {
		t.Fatalf("first consume expected success, got ok=%v err=%v", ok, err)
	}

	ok, err = svc.Consume(context.Background(), "account_verify", "u1", token)
	if !errors.Is(err, iamErrorx.ErrOneTimeTokenInvalidOrExpired) {
		t.Fatalf("expected ErrOneTimeTokenInvalidOrExpired, got %v", err)
	}
	if ok {
		t.Fatal("second consume must be false")
	}
}

func TestOneTimeTokenServiceCacheError(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Security.OneTimeTokenTTL = time.Minute

	svc := iamSvcImpl.NewOneTimeTokenService(cfg, &oneTimeTokenCacheMock{
		setFn: func(ctx context.Context, purpose string, userID string, tokenHash string, ttl time.Duration) error {
			return iamCache.ErrOneTimeTokenCacheUnavailable
		},
	})

	_, _, err := svc.Issue(context.Background(), "account_verify", "u1")
	if !errors.Is(err, iamErrorx.ErrOneTimeTokenIssueFailed) {
		t.Fatalf("expected ErrOneTimeTokenIssueFailed, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Reason != iamErrorx.ReasonOneTimeTokenIssueDependencyError {
		t.Fatalf("unexpected reason: %q", appErr.Reason)
	}
}
