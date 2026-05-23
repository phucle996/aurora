package integration_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"controlplane/internal/iam/cache"
	"controlplane/internal/iam/domain/entity"
	iamErrorx "controlplane/internal/iam/errorx"
	iamRepoImpl "controlplane/internal/iam/repository"
	iamSvcImpl "controlplane/internal/iam/service"
	testutil "controlplane/internal/iam/test/testutil"
	"controlplane/internal/security"

	goredis "github.com/redis/go-redis/v9"
)

func TestIAMMigrationsApplyOnRealPostgres(t *testing.T) {
	cfg := testutil.NewIAMTestConfig(testutil.UniqueSchema("iam_it_migrate"))
	testutil.SetRuntimeMasterKeyFromConfig(t, cfg)
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareIAMSchema(t, cfg, db)
}

func TestRegisterAccountIntegrationSuccessWithRealPostgresRedis(t *testing.T) {
	cfg := testutil.NewIAMTestConfig(testutil.UniqueSchema("iam_it_success"))
	testutil.SetRuntimeMasterKeyFromConfig(t, cfg)
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareIAMSchema(t, cfg, db)
	rdb := testutil.OpenRedis(t, cfg)

	presence := iamCache.NewRegisterPresenceCache(rdb)
	repo := iamRepoImpl.NewAuthRepository(cfg, db)
	svc := iamSvcImpl.NewAuthService(cfg, repo, nil, nil, nil, nil, presence, nil, nil, nil)

	ctx := context.Background()
	username, email := testutil.UniqueIdentity("reg_success")
	err := svc.RegisterAccount(ctx, iamEntity.User{Username: username, Email: email}, iamEntity.UserProfile{Fullname: "Integration User"}, "secret123")
	if err != nil {
		t.Fatalf("first register should succeed: %v", err)
	}

	if count := testutil.CountUsersByIdentity(ctx, t, db, cfg.SchemaSQL.IAM, username, email); count != 1 {
		t.Fatalf("expected 1 user row, got %d", count)
	}

	usernameHit, emailHit, err := presence.Check(ctx, username, email)
	if err != nil {
		t.Fatalf("presence check after success: %v", err)
	}
	if !usernameHit || !emailHit {
		t.Fatalf("expected bitmap set for username and email, got username=%v email=%v", usernameHit, emailHit)
	}
}

func TestRegisterAccountIntegrationDuplicateWithRealPostgresRedis(t *testing.T) {
	cfg := testutil.NewIAMTestConfig(testutil.UniqueSchema("iam_it_duplicate"))
	testutil.SetRuntimeMasterKeyFromConfig(t, cfg)
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareIAMSchema(t, cfg, db)
	rdb := testutil.OpenRedis(t, cfg)

	presence := iamCache.NewRegisterPresenceCache(rdb)
	repo := iamRepoImpl.NewAuthRepository(cfg, db)
	svc := iamSvcImpl.NewAuthService(cfg, repo, nil, nil, nil, nil, presence, nil, nil, nil)

	ctx := context.Background()
	username, email := testutil.UniqueIdentity("reg_duplicate")
	if err := svc.RegisterAccount(ctx, iamEntity.User{Username: username, Email: email}, iamEntity.UserProfile{Fullname: "Integration User"}, "secret123"); err != nil {
		t.Fatalf("seed register should succeed: %v", err)
	}

	err := svc.RegisterAccount(ctx, iamEntity.User{Username: username, Email: email}, iamEntity.UserProfile{Fullname: "Integration User"}, "secret123")
	if !errors.Is(err, iamErrorx.ErrUserAlreadyExist) {
		t.Fatalf("expected ErrUserAlreadyExist on duplicate, got %v", err)
	}
	if count := testutil.CountUsersByIdentity(ctx, t, db, cfg.SchemaSQL.IAM, username, email); count != 1 {
		t.Fatalf("expected still 1 user row after duplicate, got %d", count)
	}
}

func TestRegisterAccountIntegrationBitmapFalsePositiveStillCreatesUser(t *testing.T) {
	cfg := testutil.NewIAMTestConfig(testutil.UniqueSchema("iam_it_false_positive"))
	testutil.SetRuntimeMasterKeyFromConfig(t, cfg)
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareIAMSchema(t, cfg, db)
	rdb := testutil.OpenRedis(t, cfg)

	presence := iamCache.NewRegisterPresenceCache(rdb)
	repo := iamRepoImpl.NewAuthRepository(cfg, db)
	svc := iamSvcImpl.NewAuthService(cfg, repo, nil, nil, nil, nil, presence, nil, nil, nil)

	ctx := context.Background()
	username, email := testutil.UniqueIdentity("reg_false_positive")
	if err := presence.MarkExists(ctx, username, email); err != nil {
		t.Fatalf("seed bitmap false positive: %v", err)
	}

	err := svc.RegisterAccount(ctx, iamEntity.User{Username: username, Email: email}, iamEntity.UserProfile{Fullname: "Integration User"}, "secret123")
	if err != nil {
		t.Fatalf("register should still succeed on bitmap false positive: %v", err)
	}
	if count := testutil.CountUsersByIdentity(ctx, t, db, cfg.SchemaSQL.IAM, username, email); count != 1 {
		t.Fatalf("expected 1 user row after false positive path, got %d", count)
	}
}

func TestRegisterAccountIntegrationRedisFallbackStillCreatesUser(t *testing.T) {
	cfg := testutil.NewIAMTestConfig(testutil.UniqueSchema("iam_it_redis_fallback"))
	testutil.SetRuntimeMasterKeyFromConfig(t, cfg)
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareIAMSchema(t, cfg, db)

	brokenRedis := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:0"})
	defer brokenRedis.Close()

	presence := iamCache.NewRegisterPresenceCache(brokenRedis)
	repo := iamRepoImpl.NewAuthRepository(cfg, db)
	svc := iamSvcImpl.NewAuthService(cfg, repo, nil, nil, nil, nil, presence, nil, nil, nil)

	ctx := context.Background()
	username, email := testutil.UniqueIdentity("reg_redis_fallback")
	err := svc.RegisterAccount(ctx, iamEntity.User{Username: username, Email: email}, iamEntity.UserProfile{Fullname: "Integration User"}, "secret123")
	if err != nil {
		t.Fatalf("register should succeed when redis is unavailable: %v", err)
	}
	if count := testutil.CountUsersByIdentity(ctx, t, db, cfg.SchemaSQL.IAM, username, email); count != 1 {
		t.Fatalf("expected 1 user row after redis fallback, got %d", count)
	}
}

func TestRegisterAccountIntegrationDuplicateMarksBitmapAfterDBConflict(t *testing.T) {
	cfg := testutil.NewIAMTestConfig(testutil.UniqueSchema("iam_it_duplicate_mark"))
	testutil.SetRuntimeMasterKeyFromConfig(t, cfg)
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareIAMSchema(t, cfg, db)
	rdb := testutil.OpenRedis(t, cfg)

	presence := iamCache.NewRegisterPresenceCache(rdb)
	repo := iamRepoImpl.NewAuthRepository(cfg, db)
	svc := iamSvcImpl.NewAuthService(cfg, repo, nil, nil, nil, nil, presence, nil, nil, nil)

	ctx := context.Background()
	username, email := testutil.UniqueIdentity("reg_duplicate_mark")
	insertSQL := fmt.Sprintf("INSERT INTO %s.users (id, username, email, phone, password_hash, status, created_at, updated_at) VALUES (gen_random_uuid(), $1, $2, NULL, $3, 'pending-active', now(), now())", cfg.SchemaSQL.IAM)
	if _, err := db.Exec(ctx, insertSQL, username, email, "hashed-password"); err != nil {
		t.Fatalf("seed user directly: %v", err)
	}

	err := svc.RegisterAccount(ctx, iamEntity.User{Username: username, Email: email}, iamEntity.UserProfile{Fullname: "Integration User"}, "secret123")
	if !errors.Is(err, iamErrorx.ErrUserAlreadyExist) {
		t.Fatalf("expected ErrUserAlreadyExist after DB conflict, got %v", err)
	}

	usernameHit, emailHit, err := presence.Check(ctx, username, email)
	if err != nil {
		t.Fatalf("presence check after duplicate conflict: %v", err)
	}
	if !usernameHit || !emailHit {
		t.Fatalf("expected bitmap set after duplicate conflict, got username=%v email=%v", usernameHit, emailHit)
	}
}

func TestLoginIntegrationSuccessWithRealPostgres(t *testing.T) {
	cfg := testutil.NewIAMTestConfig(testutil.UniqueSchema("iam_it_login_success"))
	testutil.SetRuntimeMasterKeyFromConfig(t, cfg)
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareIAMSchema(t, cfg, db)
	rdb := testutil.OpenRedis(t, cfg)

	presence := iamCache.NewRegisterPresenceCache(rdb)
	repo := iamRepoImpl.NewAuthRepository(cfg, db)
	deviceRepo := iamRepoImpl.NewDeviceRepository(cfg, db)
	registerSvc := iamSvcImpl.NewAuthService(cfg, repo, nil, deviceRepo, nil, nil, presence, nil, nil, nil)
	username, email := testutil.UniqueIdentity("login_success")
	if err := registerSvc.RegisterAccount(context.Background(), iamEntity.User{Username: username, Email: email}, iamEntity.UserProfile{Fullname: "Login User"}, "secret123"); err != nil {
		t.Fatalf("seed register should succeed: %v", err)
	}
	if _, err := db.Exec(context.Background(), fmt.Sprintf("UPDATE %s.users SET status = 'active' WHERE username = $1", cfg.SchemaSQL.IAM), username); err != nil {
		t.Fatalf("activate user: %v", err)
	}

	loginSvc := iamSvcImpl.NewAuthService(cfg, repo, nil, deviceRepo, nil, nil, presence, &integrationSecretProvider{}, nil, nil)
	result, err := loginSvc.Login(context.Background(), iamEntity.LoginRequest{Username: username, Password: "secret123", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	if err != nil {
		t.Fatalf("login should succeed: %v", err)
	}
	if result == nil || result.AccessToken == "" || result.RefreshToken == "" {
		t.Fatalf("expected tokens, got %#v", result)
	}
	if len(strings.Split(result.RefreshToken, ".")) == 3 {
		t.Fatalf("expected opaque refresh token, got jwt-like token %q", result.RefreshToken)
	}
	var count int
	if err := db.QueryRow(context.Background(), fmt.Sprintf("SELECT count(*) FROM %s.refresh_tokens rt JOIN %s.users u ON u.id = rt.user_id WHERE u.username = $1", cfg.SchemaSQL.IAM, cfg.SchemaSQL.IAM), username).Scan(&count); err != nil {
		t.Fatalf("count refresh tokens: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 refresh session row, got %d", count)
	}
}

func TestLoginIntegrationPendingActiveBlocked(t *testing.T) {
	cfg := testutil.NewIAMTestConfig(testutil.UniqueSchema("iam_it_login_pending"))
	testutil.SetRuntimeMasterKeyFromConfig(t, cfg)
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareIAMSchema(t, cfg, db)
	rdb := testutil.OpenRedis(t, cfg)

	presence := iamCache.NewRegisterPresenceCache(rdb)
	repo := iamRepoImpl.NewAuthRepository(cfg, db)
	svc := iamSvcImpl.NewAuthService(cfg, repo, nil, nil, nil, nil, presence, &integrationSecretProvider{}, nil, nil)
	username, email := testutil.UniqueIdentity("login_pending")
	if err := svc.RegisterAccount(context.Background(), iamEntity.User{Username: username, Email: email}, iamEntity.UserProfile{Fullname: "Pending User"}, "secret123"); err != nil {
		t.Fatalf("seed register should succeed: %v", err)
	}
	_, err := svc.Login(context.Background(), iamEntity.LoginRequest{Username: username, Password: "secret123", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	if !errors.Is(err, iamErrorx.ErrVerificationRequired) {
		t.Fatalf("expected verification required, got %v", err)
	}
	var count int
	if err := db.QueryRow(context.Background(), fmt.Sprintf("SELECT count(*) FROM %s.refresh_tokens", cfg.SchemaSQL.IAM)).Scan(&count); err != nil {
		t.Fatalf("count refresh tokens: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 refresh sessions, got %d", count)
	}
}

type integrationSecretProvider struct{}

func (integrationSecretProvider) GetPrimary(ctx context.Context, family string) (security.SecretCandidate, error) {
	return security.SecretCandidate{Family: family, Value: family + "-secret", IsPrimary: true}, nil
}
func (integrationSecretProvider) GetCandidates(ctx context.Context, family string) ([]security.SecretCandidate, error) {
	candidate, _ := integrationSecretProvider{}.GetPrimary(ctx, family)
	return []security.SecretCandidate{candidate}, nil
}
func (integrationSecretProvider) Warm(ctx context.Context, family string) error { return nil }
func (integrationSecretProvider) Invalidate(family string)                      {}
