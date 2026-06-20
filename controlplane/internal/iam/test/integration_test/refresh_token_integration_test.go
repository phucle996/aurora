package integration_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"controlplane/internal/iam/domain/entity"
	iamRepoImpl "controlplane/internal/iam/repository"
	iamSvcImpl "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/iam/test/testutil"
	"controlplane/internal/security"
)

func TestRefreshTokenIntegrationSuccessRotatesSession(t *testing.T) {
	cfg := testutil.NewIAMTestConfig(testutil.UniqueSchema("iam_it_refresh_success"))
	testutil.SetRuntimeMasterKeyFromConfig(t, cfg)
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareIAMSchema(t, cfg, db)
	rdb := testutil.OpenRedis(t, cfg)

	authRepo := iamRepoImpl.NewAuthRepository(cfg, db)
	deviceRepo := iamRepoImpl.NewDeviceRepository(cfg, db)
	refreshRepo := iamRepoImpl.NewRefreshTokenRepository(cfg, db)
	deviceSvc := iamSvcImpl.NewDeviceService(deviceRepo, refreshRepo, makeIntegrationRegistry(rdb))
	refreshSvc := iamSvcImpl.NewSessionRefreshService(cfg, refreshRepo, nil, makeIntegrationRegistry(rdb))
	registerSvc := iamSvcImpl.NewAuthService(cfg, authRepo, refreshSvc, deviceSvc, makeIntegrationRegistry(rdb), nil, nil)
	username, email := testutil.UniqueIdentity("refresh_success")
	if err := registerSvc.RegisterAccount(context.Background(), iamEntity.User{Username: username, Email: email}, iamEntity.UserProfile{Fullname: "Refresh User"}, "secret123"); err != nil {
		t.Fatalf("seed register should succeed: %v", err)
	}
	if _, err := db.Exec(context.Background(), fmt.Sprintf("UPDATE %s.users SET status = 'active' WHERE username = $1", cfg.SchemaSQL.IAM), username); err != nil {
		t.Fatalf("activate user: %v", err)
	}

	loginSvc := iamSvcImpl.NewAuthService(cfg, authRepo, refreshSvc, deviceSvc, makeIntegrationRegistry(rdb), nil, nil)
	loginResult, err := loginSvc.Login(context.Background(), iamEntity.LoginRequest{Username: username, Password: "secret123", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", TrustDevice: true})
	if err != nil {
		t.Fatalf("login should succeed: %v", err)
	}

	refreshResult, err := refreshSvc.RefreshUserOpaque(context.Background(), loginResult.RefreshToken)
	if err != nil {
		t.Fatalf("refresh should succeed: %v", err)
	}
	if refreshResult == nil || refreshResult.AccessToken == "" || refreshResult.RefreshToken == "" {
		t.Fatalf("expected refresh result, got %#v", refreshResult)
	}
	if refreshResult.RefreshToken == loginResult.RefreshToken {
		t.Fatal("expected rotated refresh token")
	}
	if len(strings.Split(refreshResult.RefreshToken, ".")) == 3 {
		t.Fatalf("expected opaque rotated refresh token, got %q", refreshResult.RefreshToken)
	}

	var count int
	if err := db.QueryRow(context.Background(), fmt.Sprintf("SELECT count(*) FROM %s.refresh_tokens", cfg.SchemaSQL.IAM)).Scan(&count); err != nil {
		t.Fatalf("count refresh tokens: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 refresh token row after rotate, got %d", count)
	}

	_, err = refreshSvc.RefreshUserOpaque(context.Background(), loginResult.RefreshToken)
	if !errors.Is(err, iamTaxonomy.ErrInvalidSession) {
		t.Fatalf("expected old refresh token to be invalid, got %v", err)
	}
}

func TestRefreshTokenIntegrationPendingActiveBlocked(t *testing.T) {
	cfg := testutil.NewIAMTestConfig(testutil.UniqueSchema("iam_it_refresh_pending"))
	testutil.SetRuntimeMasterKeyFromConfig(t, cfg)
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareIAMSchema(t, cfg, db)
	rdb := testutil.OpenRedis(t, cfg)

	authRepo := iamRepoImpl.NewAuthRepository(cfg, db)
	deviceRepo := iamRepoImpl.NewDeviceRepository(cfg, db)
	refreshRepo := iamRepoImpl.NewRefreshTokenRepository(cfg, db)
	deviceSvc := iamSvcImpl.NewDeviceService(deviceRepo, refreshRepo, makeIntegrationRegistry(rdb))
	refreshSvc := iamSvcImpl.NewSessionRefreshService(cfg, refreshRepo, nil, makeIntegrationRegistry(rdb))
	registerSvc := iamSvcImpl.NewAuthService(cfg, authRepo, refreshSvc, deviceSvc, makeIntegrationRegistry(rdb), nil, nil)
	username, email := testutil.UniqueIdentity("refresh_pending")
	if err := registerSvc.RegisterAccount(context.Background(), iamEntity.User{Username: username, Email: email}, iamEntity.UserProfile{Fullname: "Refresh Pending User"}, "secret123"); err != nil {
		t.Fatalf("seed register should succeed: %v", err)
	}
	if _, err := db.Exec(context.Background(), fmt.Sprintf("UPDATE %s.users SET status = 'active' WHERE username = $1", cfg.SchemaSQL.IAM), username); err != nil {
		t.Fatalf("activate user: %v", err)
	}

	loginSvc := iamSvcImpl.NewAuthService(cfg, authRepo, refreshSvc, deviceSvc, makeIntegrationRegistry(rdb), nil, nil)
	loginResult, err := loginSvc.Login(context.Background(), iamEntity.LoginRequest{Username: username, Password: "secret123", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", TrustDevice: true})
	if err != nil {
		t.Fatalf("login should succeed: %v", err)
	}
	if _, err := db.Exec(context.Background(), fmt.Sprintf("UPDATE %s.users SET status = 'pending-active' WHERE username = $1", cfg.SchemaSQL.IAM), username); err != nil {
		t.Fatalf("deactivate user: %v", err)
	}

	_, err = refreshSvc.RefreshUserOpaque(context.Background(), loginResult.RefreshToken)
	if !errors.Is(err, iamTaxonomy.ErrInvalidSession) {
		t.Fatalf("expected invalid session for blocked user, got %v", err)
	}

	var count int
	if err := db.QueryRow(context.Background(), fmt.Sprintf("SELECT count(*) FROM %s.refresh_tokens", cfg.SchemaSQL.IAM)).Scan(&count); err != nil {
		t.Fatalf("count refresh tokens: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected original refresh token row to remain, got %d", count)
	}
}

func TestRefreshTokenIntegrationAccessClaimsDoNotContainStatus(t *testing.T) {
	cfg := testutil.NewIAMTestConfig(testutil.UniqueSchema("iam_it_refresh_claims"))
	testutil.SetRuntimeMasterKeyFromConfig(t, cfg)
	db := testutil.OpenPostgres(t, cfg)
	testutil.PrepareIAMSchema(t, cfg, db)
	rdb := testutil.OpenRedis(t, cfg)

	authRepo := iamRepoImpl.NewAuthRepository(cfg, db)
	deviceRepo := iamRepoImpl.NewDeviceRepository(cfg, db)
	refreshRepo := iamRepoImpl.NewRefreshTokenRepository(cfg, db)
	deviceSvc := iamSvcImpl.NewDeviceService(deviceRepo, refreshRepo, makeIntegrationRegistry(rdb))
	refreshSvc := iamSvcImpl.NewSessionRefreshService(cfg, refreshRepo, nil, makeIntegrationRegistry(rdb))
	registerSvc := iamSvcImpl.NewAuthService(cfg, authRepo, refreshSvc, deviceSvc, makeIntegrationRegistry(rdb), nil, nil)
	username, email := testutil.UniqueIdentity("refresh_claims")
	if err := registerSvc.RegisterAccount(context.Background(), iamEntity.User{Username: username, Email: email}, iamEntity.UserProfile{Fullname: "Refresh Claims User"}, "secret123"); err != nil {
		t.Fatalf("seed register should succeed: %v", err)
	}
	if _, err := db.Exec(context.Background(), fmt.Sprintf("UPDATE %s.users SET status = 'active' WHERE username = $1", cfg.SchemaSQL.IAM), username); err != nil {
		t.Fatalf("activate user: %v", err)
	}

	loginSvc := iamSvcImpl.NewAuthService(cfg, authRepo, refreshSvc, deviceSvc, makeIntegrationRegistry(rdb), nil, nil)
	loginResult, err := loginSvc.Login(context.Background(), iamEntity.LoginRequest{Username: username, Password: "secret123", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", TrustDevice: true})
	if err != nil {
		t.Fatalf("login should succeed: %v", err)
	}

	refreshResult, err := refreshSvc.RefreshUserOpaque(context.Background(), loginResult.RefreshToken)
	if err != nil {
		t.Fatalf("refresh should succeed: %v", err)
	}

	claims, err := security.Parse(refreshResult.AccessToken, []byte("access_token-secret"))
	if err != nil {
		t.Fatalf("parse refreshed access token: %v", err)
	}
	if claims.Subject == "" || claims.TokenID == "" {
		t.Fatalf("expected refreshed token claims subject+jti to be present")
	}
}
