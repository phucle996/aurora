package svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"controlplane/internal/config"
	iamSvcImpl "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
)

type oneTimeTokenCacheMock struct {
	setFn     func(ctx context.Context, purpose string, userID uuid.UUID, tokenHash string, ttl time.Duration) error
	consumeFn func(ctx context.Context, purpose string, userID uuid.UUID, tokenHash string) (bool, error)
}

func (m *oneTimeTokenCacheMock) SetHashedToken(ctx context.Context, purpose string, userID uuid.UUID, tokenHash string, ttl time.Duration) error {
	if m.setFn != nil {
		return m.setFn(ctx, purpose, userID, tokenHash, ttl)
	}
	return nil
}

func (m *oneTimeTokenCacheMock) ConsumeHashedToken(ctx context.Context, purpose string, userID uuid.UUID, tokenHash string) (bool, error) {
	if m.consumeFn != nil {
		return m.consumeFn(ctx, purpose, userID, tokenHash)
	}
	return false, nil
}

func TestOneTimeTokenServiceIssueAndConsumeSuccess(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Security.OneTimeTokenTTL = 10 * time.Minute

	userID := uuid.Must(uuid.NewV7())
	var storedHash string
	cacheMock := &oneTimeTokenCacheMock{
		setFn: func(ctx context.Context, purpose string, uID uuid.UUID, tokenHash string, ttl time.Duration) error {
			if purpose != "account_verify" || uID != userID {
				t.Fatalf("unexpected purpose/user: %s/%s", purpose, uID)
			}
			if ttl != cfg.Security.OneTimeTokenTTL {
				t.Fatalf("unexpected ttl: %v", ttl)
			}
			storedHash = tokenHash
			return nil
		},
		consumeFn: func(ctx context.Context, purpose string, uID uuid.UUID, tokenHash string) (bool, error) {
			return tokenHash == storedHash, nil
		},
	}

	svc := iamSvcImpl.NewOneTimeTokenService(cfg, cacheMock)
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
	svc := iamSvcImpl.NewOneTimeTokenService(cfg, &oneTimeTokenCacheMock{})

	userID := uuid.Must(uuid.NewV7())
	_, _, err := svc.Issue(context.Background(), "", userID)
	if !errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
		t.Fatalf("expected ErrOneTimeTokenInvalidPurposeOrUser, got %v", err)
	}

	_, err = svc.Consume(context.Background(), "account_verify", uuid.Nil, "abc")
	if !errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
		t.Fatalf("expected ErrOneTimeTokenInvalidPurposeOrUser, got %v", err)
	}
}

func TestOneTimeTokenServiceInvalidTTL(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Security.OneTimeTokenTTL = 0
	svc := iamSvcImpl.NewOneTimeTokenService(cfg, &oneTimeTokenCacheMock{})

	userID := uuid.Must(uuid.NewV7())
	_, _, err := svc.Issue(context.Background(), "account_verify", userID)
	if !errors.Is(err, iamTaxonomy.ErrTokenIssueFailed) {
		t.Fatalf("expected ErrOneTimeTokenIssueFailed, got %v", err)
	}
}

func TestOneTimeTokenServiceConsumeTwice(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Security.OneTimeTokenTTL = time.Minute

	userID := uuid.Must(uuid.NewV7())
	used := false
	var storedHash string
	cacheMock := &oneTimeTokenCacheMock{
		setFn: func(ctx context.Context, purpose string, uID uuid.UUID, tokenHash string, ttl time.Duration) error {
			storedHash = tokenHash
			return nil
		},
		consumeFn: func(ctx context.Context, purpose string, uID uuid.UUID, tokenHash string) (bool, error) {
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
		t.Fatalf("expected ErrOneTimeTokenInvalidOrExpired, got %v", err)
	}
	if ok {
		t.Fatal("second consume must be false")
	}
}

func TestOneTimeTokenServiceCacheError(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Security.OneTimeTokenTTL = time.Minute

	userID := uuid.Must(uuid.NewV7())
	svc := iamSvcImpl.NewOneTimeTokenService(cfg, &oneTimeTokenCacheMock{
		setFn: func(ctx context.Context, purpose string, uID uuid.UUID, tokenHash string, ttl time.Duration) error {
			return iamTaxonomy.ErrGetL1CacheFailed
		},
	})

	_, _, err := svc.Issue(context.Background(), "account_verify", userID)
	if !errors.Is(err, iamTaxonomy.ErrTokenIssueFailed) {
		t.Fatalf("expected ErrOneTimeTokenIssueFailed, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Outcome != "cache_unavailable" {
		t.Fatalf("unexpected outcome: %q", appErr.Outcome)
	}
}
