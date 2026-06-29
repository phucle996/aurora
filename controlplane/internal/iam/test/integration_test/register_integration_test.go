package integration_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoImpl "controlplane/internal/iam/repository"
	iamSvcImpl "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	testutil "controlplane/internal/iam/test/testutil"
	"controlplane/internal/security"
	"controlplane/pkg/id"

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

	presence := &testRegisterPresenceCache{rdb: rdb}
	repo := iamRepoImpl.NewAuthRepository(cfg, db)
	svc := iamSvcImpl.NewAuthService(cfg, repo, nil, nil, nil, makeIntegrationRegistry(rdb), nil, nil, &testutil.SessionServiceClientMock{})

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

	repo := iamRepoImpl.NewAuthRepository(cfg, db)
	svc := iamSvcImpl.NewAuthService(cfg, repo, nil, nil, nil, makeIntegrationRegistry(rdb), nil, nil, &testutil.SessionServiceClientMock{})

	ctx := context.Background()
	username, email := testutil.UniqueIdentity("reg_duplicate")
	if err := svc.RegisterAccount(ctx, iamEntity.User{Username: username, Email: email}, iamEntity.UserProfile{Fullname: "Integration User"}, "secret123"); err != nil {
		t.Fatalf("seed register should succeed: %v", err)
	}

	err := svc.RegisterAccount(ctx, iamEntity.User{Username: username, Email: email}, iamEntity.UserProfile{Fullname: "Integration User"}, "secret123")
	if !errors.Is(err, iamTaxonomy.ErrUserAlreadyExist) {
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

	presence := &testRegisterPresenceCache{rdb: rdb}
	repo := iamRepoImpl.NewAuthRepository(cfg, db)
	svc := iamSvcImpl.NewAuthService(cfg, repo, nil, nil, nil, makeIntegrationRegistry(rdb), nil, nil, &testutil.SessionServiceClientMock{})

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

	repo := iamRepoImpl.NewAuthRepository(cfg, db)
	svc := iamSvcImpl.NewAuthService(cfg, repo, nil, nil, nil, makeIntegrationRegistry(brokenRedis), nil, nil, &testutil.SessionServiceClientMock{})

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

	presence := &testRegisterPresenceCache{rdb: rdb}
	repo := iamRepoImpl.NewAuthRepository(cfg, db)
	svc := iamSvcImpl.NewAuthService(cfg, repo, nil, nil, nil, makeIntegrationRegistry(rdb), nil, nil, &testutil.SessionServiceClientMock{})

	ctx := context.Background()
	username, email := testutil.UniqueIdentity("reg_duplicate_mark")
	insertSQL := fmt.Sprintf("INSERT INTO %s.users (id, username, email, phone, password_hash, status, created_at, updated_at) VALUES (gen_random_uuid(), $1, $2, NULL, $3, 'pending-active', now(), now())", cfg.SchemaSQL.IAM)
	if _, err := db.Exec(ctx, insertSQL, username, email, "hashed-password"); err != nil {
		t.Fatalf("seed user directly: %v", err)
	}

	err := svc.RegisterAccount(ctx, iamEntity.User{Username: username, Email: email}, iamEntity.UserProfile{Fullname: "Integration User"}, "secret123")
	if !errors.Is(err, iamTaxonomy.ErrUserAlreadyExist) {
		t.Fatalf("expected ErrUserAlreadyExist after DB conflict, got %v", err)
	}
	svc.Stop()

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

	repo := iamRepoImpl.NewAuthRepository(cfg, db)
	deviceRepo := iamRepoImpl.NewDeviceRepository(cfg, db)
	refreshRepo := iamRepoImpl.NewRefreshTokenRepository(cfg, db)
	rbacRepo := iamRepoImpl.NewRbacRepository(cfg, db)
	refreshSvc := iamSvcImpl.NewSessionRefreshService(cfg, refreshRepo, rbacRepo, makeIntegrationRegistry(rdb))
	deviceSvc := iamSvcImpl.NewDeviceService(deviceRepo, refreshRepo, makeIntegrationRegistry(rdb), &testutil.SessionServiceClientMock{})
	registerSvc := iamSvcImpl.NewAuthService(cfg, repo, rbacRepo, refreshSvc, deviceSvc, makeIntegrationRegistry(rdb), nil, nil, &testutil.SessionServiceClientMock{})
	username, email := testutil.UniqueIdentity("login_success")
	if err := registerSvc.RegisterAccount(context.Background(), iamEntity.User{Username: username, Email: email}, iamEntity.UserProfile{Fullname: "Login User"}, "secret123"); err != nil {
		t.Fatalf("seed register should succeed: %v", err)
	}
	if _, err := db.Exec(context.Background(), fmt.Sprintf("UPDATE %s.users SET status = 'active' WHERE username = $1", cfg.SchemaSQL.IAM), username); err != nil {
		t.Fatalf("activate user: %v", err)
	}

	loginSvc := iamSvcImpl.NewAuthService(cfg, repo, rbacRepo, refreshSvc, deviceSvc, makeIntegrationRegistry(rdb), nil, nil, nil)
	res, err := loginSvc.VerifyUserCredentials(context.Background(), iamEntity.LoginRequest{Username: username, Password: "secret123", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", TrustDevice: true})
	if err != nil {
		t.Fatalf("login should succeed: %v", err)
	}
	if !res.Valid {
		t.Fatal("expected login result to be valid")
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

	repo := iamRepoImpl.NewAuthRepository(cfg, db)
	svc := iamSvcImpl.NewAuthService(cfg, repo, nil, nil, nil, makeIntegrationRegistry(rdb), nil, nil, nil)
	username, email := testutil.UniqueIdentity("login_pending")
	if err := svc.RegisterAccount(context.Background(), iamEntity.User{Username: username, Email: email}, iamEntity.UserProfile{Fullname: "Pending User"}, "secret123"); err != nil {
		t.Fatalf("seed register should succeed: %v", err)
	}
	svc.Stop()
	_, err := svc.VerifyUserCredentials(context.Background(), iamEntity.LoginRequest{Username: username, Password: "secret123", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	if !errors.Is(err, iamTaxonomy.ErrVerificationRequired) {
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

func makeIntegrationRegistry(rdb *goredis.Client) *cacheengine.CacheRegistry {
	l1Cache := cacheengine.NewShardedCache()
	registry := cacheengine.NewCacheRegistry(l1Cache)
	if rdb != nil {
		registry.L2 = cacheengine.NewL2Cache(rdb)
		registry.Exec = cacheengine.NewL2LuaExecutor(rdb)
	}
	cacheengine.Register(registry, "zone_by_code", 5*time.Minute, func(ctx context.Context, param string) (string, error) {
		return "00000000-0000-0000-0000-000000000000", nil
	})
	return registry
}

type testRegisterPresenceCache struct {
	rdb *goredis.Client
}

func (c *testRegisterPresenceCache) Check(ctx context.Context, username string, email string) (bool, bool, error) {
	usernameDigest, err := security.PresenceHMACSHA256Hex("iam.register.username", username)
	if err != nil {
		return false, false, err
	}
	emailDigest, err := security.PresenceHMACSHA256Hex("iam.register.email", email)
	if err != nil {
		return false, false, err
	}
	usernameHit, err := c.rdb.GetBit(ctx, "iam:register:bitmap:username", id.BitmapIndex(usernameDigest)).Result()
	if err != nil {
		return false, false, err
	}
	emailHit, err := c.rdb.GetBit(ctx, "iam:register:bitmap:email", id.BitmapIndex(emailDigest)).Result()
	if err != nil {
		return false, false, err
	}
	return usernameHit == 1, emailHit == 1, nil
}

func (c *testRegisterPresenceCache) MarkExists(ctx context.Context, username string, email string) error {
	usernameDigest, err := security.PresenceHMACSHA256Hex("iam.register.username", username)
	if err != nil {
		return err
	}
	emailDigest, err := security.PresenceHMACSHA256Hex("iam.register.email", email)
	if err != nil {
		return err
	}
	pipe := c.rdb.Pipeline()
	pipe.SetBit(ctx, "iam:register:bitmap:username", id.BitmapIndex(usernameDigest), 1)
	pipe.SetBit(ctx, "iam:register:bitmap:email", id.BitmapIndex(emailDigest), 1)
	_, err = pipe.Exec(ctx)
	return err
}
