package svc_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	coreEntity "controlplane/internal/core/domain/entity"
	iamCache "controlplane/internal/iam/cache"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamSvcImpl "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type authRepoMock struct {
	checkFn         func(ctx context.Context, username string, email string) (bool, error)
	createFn        func(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error
	getUserFn       func(ctx context.Context, username string) (*iamEntity.LoginUser, error)
	createRefreshFn func(ctx context.Context, token iamEntity.RefreshToken) error
}

var _ iamRepoInterface.AuthRepository = (*authRepoMock)(nil)

func (m *authRepoMock) CheckUserExist(ctx context.Context, username string, email string) (bool, error) {
	if m.checkFn != nil {
		return m.checkFn(ctx, username, email)
	}
	return false, nil
}

func (m *authRepoMock) GetLoginUserByUsername(ctx context.Context, username string) (*iamEntity.LoginUser, error) {
	if m.getUserFn != nil {
		return m.getUserFn(ctx, username)
	}
	return nil, nil
}

func (m *authRepoMock) CreateRegisteredUser(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error {
	if m.createFn != nil {
		return m.createFn(ctx, user, profile)
	}
	return nil
}

func (m *authRepoMock) CreateRefreshTokenSession(ctx context.Context, token iamEntity.RefreshToken) error {
	if m.createRefreshFn != nil {
		return m.createRefreshFn(ctx, token)
	}
	return nil
}

var _ iamCache.RegisterPresenceCache = (*presenceCacheMock)(nil)

type presenceCacheMock struct {
	checkFn func(ctx context.Context, username string, email string) (bool, bool, error)
	markFn  func(ctx context.Context, username string, email string) error
}

func (m *presenceCacheMock) Check(ctx context.Context, username string, email string) (bool, bool, error) {
	if m.checkFn != nil {
		return m.checkFn(ctx, username, email)
	}
	return false, false, nil
}

func (m *presenceCacheMock) MarkExists(ctx context.Context, username string, email string) error {
	if m.markFn != nil {
		return m.markFn(ctx, username, email)
	}
	return nil
}

func makeTestRegistry(secretKey string) *cacheengine.CacheRegistry {
	l1Cache := cacheengine.NewShardedCache()
	registry := cacheengine.NewCacheRegistry(l1Cache)
	cacheengine.Register(registry, "access_secret", 1*time.Hour, func(ctx context.Context, param string) (*coreEntity.RuntimeSecrets, error) {
		return &coreEntity.RuntimeSecrets{
			SecretType: "access_secret",
			Active: coreEntity.RuntimeSecret{
				Secret:      []byte(secretKey),
				Fingerprint: "fp",
			},
			Standby: coreEntity.RuntimeSecret{
				Secret:      []byte(secretKey),
				Fingerprint: "fp",
			},
		}, nil
	})
	return registry
}

func newAuthService(repo iamRepoInterface.AuthRepository, presence iamCache.RegisterPresenceCache, registry *cacheengine.CacheRegistry) iamSvcInterface.AuthService {
	return iamSvcImpl.NewAuthService(nil, repo, nil, &deviceRepoMock{}, nil, nil, presence, registry, nil, nil)
}

func TestAuthServiceRegisterAccountSuccessOnBitmapMiss(t *testing.T) {
	marked := false
	created := false
	svc := newAuthService(&authRepoMock{createFn: func(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error {
		created = true
		if user.Username != "alice.nguyen" || user.Email != "user@example.com" {
			t.Fatalf("unexpected user identity: %#v", user)
		}
		if user.Status != iamEntity.UserStatusPendingActive {
			t.Fatalf("expected pending-active status, got %q", user.Status)
		}
		return nil
	}}, &presenceCacheMock{
		checkFn: func(ctx context.Context, username string, email string) (bool, bool, error) { return false, false, nil },
		markFn:  func(ctx context.Context, username string, email string) error { marked = true; return nil },
	}, nil)

	err := svc.RegisterAccount(context.Background(), iamEntity.User{Username: "alice.nguyen", Email: "user@example.com"}, iamEntity.UserProfile{Fullname: "Alice Nguyen"}, "secret123")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !created {
		t.Fatal("expected repository create to be called")
	}
	if !marked {
		t.Fatal("expected presence cache to be marked")
	}
}

func TestAuthServiceRegisterAccountPresenceCheckErrorFallsBackToDB(t *testing.T) {
	created := false
	svc := newAuthService(&authRepoMock{createFn: func(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error {
		created = true
		return nil
	}}, &presenceCacheMock{checkFn: func(ctx context.Context, username string, email string) (bool, bool, error) {
		return false, false, fmt.Errorf("redis down")
	}}, nil)

	err := svc.RegisterAccount(context.Background(), iamEntity.User{Username: "alice.nguyen", Email: "user@example.com"}, iamEntity.UserProfile{Fullname: "Alice Nguyen"}, "secret123")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !created {
		t.Fatal("expected repository create to be called")
	}
}

func TestAuthServiceRegisterAccountBitmapHitAndUserExists(t *testing.T) {
	checked := false
	created := false
	svc := newAuthService(&authRepoMock{
		checkFn: func(ctx context.Context, username string, email string) (bool, error) {
			checked = true
			return true, nil
		},
		createFn: func(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error {
			created = true
			return nil
		},
	}, &presenceCacheMock{checkFn: func(ctx context.Context, username string, email string) (bool, bool, error) { return true, false, nil }}, nil)

	err := svc.RegisterAccount(context.Background(), iamEntity.User{Username: "alice.nguyen", Email: "user@example.com"}, iamEntity.UserProfile{Fullname: "Alice Nguyen"}, "secret123")
	if !errors.Is(err, iamTaxonomy.ErrUserAlreadyExist) {
		t.Fatalf("expected ErrUserAlreadyExist, got %v", err)
	}
	if !checked {
		t.Fatal("expected CheckUserExist to be called")
	}
	if created {
		t.Fatal("did not expect repository create to be called")
	}
}

func TestAuthServiceRegisterAccountBitmapHitFalsePositiveThenInsert(t *testing.T) {
	checked := false
	created := false
	marked := false
	svc := newAuthService(&authRepoMock{
		checkFn: func(ctx context.Context, username string, email string) (bool, error) {
			checked = true
			return false, nil
		},
		createFn: func(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error {
			created = true
			return nil
		},
	}, &presenceCacheMock{
		checkFn: func(ctx context.Context, username string, email string) (bool, bool, error) { return false, true, nil },
		markFn:  func(ctx context.Context, username string, email string) error { marked = true; return nil },
	}, nil)

	err := svc.RegisterAccount(context.Background(), iamEntity.User{Username: "alice.nguyen", Email: "user@example.com"}, iamEntity.UserProfile{Fullname: "Alice Nguyen"}, "secret123")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !checked || !created || !marked {
		t.Fatalf("expected checked=%v created=%v marked=%v all true", checked, created, marked)
	}
}

func TestAuthServiceRegisterAccountDuplicateFromRepoMarksCache(t *testing.T) {
	marked := false
	svc := newAuthService(&authRepoMock{createFn: func(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error {
		return iamTaxonomy.ErrUserAlreadyExist
	}}, &presenceCacheMock{
		checkFn: func(ctx context.Context, username string, email string) (bool, bool, error) { return false, false, nil },
		markFn:  func(ctx context.Context, username string, email string) error { marked = true; return nil },
	}, nil)

	err := svc.RegisterAccount(context.Background(), iamEntity.User{Username: "alice.nguyen", Email: "user@example.com"}, iamEntity.UserProfile{Fullname: "Alice Nguyen"}, "secret123")
	if !errors.Is(err, iamTaxonomy.ErrUserAlreadyExist) {
		t.Fatalf("expected ErrUserAlreadyExist, got %v", err)
	}
	if !marked {
		t.Fatal("expected presence cache to be marked on duplicate")
	}
}

func TestAuthServiceRegisterAccountDuplicateStillReturnsDuplicateWhenMarkFails(t *testing.T) {
	svc := newAuthService(&authRepoMock{createFn: func(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error {
		return iamTaxonomy.ErrUserAlreadyExist
	}}, &presenceCacheMock{
		checkFn: func(ctx context.Context, username string, email string) (bool, bool, error) { return false, false, nil },
		markFn:  func(ctx context.Context, username string, email string) error { return fmt.Errorf("redis down") },
	}, nil)

	err := svc.RegisterAccount(context.Background(), iamEntity.User{Username: "alice.nguyen", Email: "user@example.com"}, iamEntity.UserProfile{Fullname: "Alice Nguyen"}, "secret123")
	if !errors.Is(err, iamTaxonomy.ErrUserAlreadyExist) {
		t.Fatalf("expected ErrUserAlreadyExist, got %v", err)
	}
}

func TestAuthServiceRegisterAccountSuccessStillReturnsSuccessWhenMarkFails(t *testing.T) {
	created := false
	svc := newAuthService(&authRepoMock{createFn: func(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error {
		created = true
		return nil
	}}, &presenceCacheMock{
		checkFn: func(ctx context.Context, username string, email string) (bool, bool, error) { return false, false, nil },
		markFn:  func(ctx context.Context, username string, email string) error { return fmt.Errorf("redis down") },
	}, nil)

	err := svc.RegisterAccount(context.Background(), iamEntity.User{Username: "alice.nguyen", Email: "user@example.com"}, iamEntity.UserProfile{Fullname: "Alice Nguyen"}, "secret123")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !created {
		t.Fatal("expected repository create to be called")
	}
}

func TestAuthServiceRegisterAccountValidation(t *testing.T) {
	tests := []struct {
		name     string
		user     iamEntity.User
		profile  iamEntity.UserProfile
		password string
		want     error
	}{
		{name: "missing username", user: iamEntity.User{Username: "", Email: "user@example.com"}, profile: iamEntity.UserProfile{Fullname: "Alice"}, password: "secret123", want: iamTaxonomy.ErrInvalidArgument},
		{name: "missing email", user: iamEntity.User{Username: "alice.nguyen", Email: ""}, profile: iamEntity.UserProfile{Fullname: "Alice"}, password: "secret123", want: iamTaxonomy.ErrInvalidArgument},
		{name: "missing fullname", user: iamEntity.User{Username: "alice.nguyen", Email: "user@example.com"}, profile: iamEntity.UserProfile{Fullname: ""}, password: "secret123", want: iamTaxonomy.ErrInvalidArgument},
		{name: "missing password", user: iamEntity.User{Username: "alice.nguyen", Email: "user@example.com"}, profile: iamEntity.UserProfile{Fullname: "Alice"}, password: "", want: iamTaxonomy.ErrInvalidArgument},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := newAuthService(&authRepoMock{}, &presenceCacheMock{}, nil)
			err := svc.RegisterAccount(context.Background(), test.user, test.profile, test.password)
			if !errors.Is(err, test.want) {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
		})
	}
}

func TestAuthServiceLoginUserNotFound(t *testing.T) {
	svc := newAuthService(&authRepoMock{getUserFn: func(ctx context.Context, username string) (*iamEntity.LoginUser, error) { return nil, nil }}, nil, nil)
	_, err := svc.Login(context.Background(), iamEntity.LoginRequest{Username: "alice.nguyen", Password: "secret123", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	if !errors.Is(err, iamTaxonomy.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestAuthServiceLoginNoRowsErrorMapsInvalidCredentials(t *testing.T) {
	svc := newAuthService(&authRepoMock{getUserFn: func(ctx context.Context, username string) (*iamEntity.LoginUser, error) {
		return nil, pgx.ErrNoRows
	}}, nil, nil)
	_, err := svc.Login(context.Background(), iamEntity.LoginRequest{Username: "alice.nguyen", Password: "secret123", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	if !errors.Is(err, iamTaxonomy.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Outcome != iamTaxonomy.LoginOutcomeInvalidCredentials {
		t.Fatalf("unexpected outcome: %q", appErr.Outcome)
	}
	if !errors.Is(appErr.Cause, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows cause preserved")
	}
}

func TestAuthServiceLoginWrongPassword(t *testing.T) {
	hash, _ := security.HashPassword("secret123")
	svc := newAuthService(&authRepoMock{getUserFn: func(ctx context.Context, username string) (*iamEntity.LoginUser, error) {
		return &iamEntity.LoginUser{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Username: username, PasswordHash: hash, Status: iamEntity.UserStatusActive}, nil
	}}, nil, makeTestRegistry("secret-key"))
	_, err := svc.Login(context.Background(), iamEntity.LoginRequest{Username: "alice.nguyen", Password: "wrongpass", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	if !errors.Is(err, iamTaxonomy.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestAuthServiceLoginPendingActiveBlocked(t *testing.T) {
	hash, _ := security.HashPassword("secret123")
	svc := newAuthService(&authRepoMock{getUserFn: func(ctx context.Context, username string) (*iamEntity.LoginUser, error) {
		return &iamEntity.LoginUser{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Username: username, PasswordHash: hash, Status: iamEntity.UserStatusPendingActive}, nil
	}}, nil, makeTestRegistry("secret-key"))
	_, err := svc.Login(context.Background(), iamEntity.LoginRequest{Username: "alice.nguyen", Password: "secret123", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	if !errors.Is(err, iamTaxonomy.ErrVerificationRequired) {
		t.Fatalf("expected ErrVerificationRequired, got %v", err)
	}
}

func TestAuthServiceLoginSuccess(t *testing.T) {
	hash, _ := security.HashPassword("secret123")
	persisted := false
	svc := newAuthService(&authRepoMock{
		getUserFn: func(ctx context.Context, username string) (*iamEntity.LoginUser, error) {
			return &iamEntity.LoginUser{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Username: username, PasswordHash: hash, Status: iamEntity.UserStatusActive}, nil
		},
		createRefreshFn: func(ctx context.Context, token iamEntity.RefreshToken) error {
			persisted = true
			if token.TokenHash == "" {
				t.Fatal("expected token hash")
			}
			if token.DeviceID == nil || *token.DeviceID == uuid.Nil {
				t.Fatal("expected refresh token to carry tracked device id")
			}
			return nil
		},
	}, nil, makeTestRegistry("secret-key"))

	result, err := svc.Login(context.Background(), iamEntity.LoginRequest{Username: "alice.nguyen", Password: "secret123", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if result == nil || result.AccessToken == "" || result.RefreshToken == "" {
		t.Fatalf("expected tokens in result, got %#v", result)
	}
	if result.RuntimeDeviceID == "" || result.TrackedDeviceID == "" {
		t.Fatalf("expected runtime and tracked device ids, got %#v", result)
	}
	if persisted == false {
		t.Fatal("expected refresh session to be persisted")
	}
	claims, err := security.Parse(result.AccessToken, []byte("secret-key"))
	if err != nil {
		t.Fatalf("expected parsable access token, got %v", err)
	}
	if claims.AccessKey != result.RuntimeDeviceID {
		t.Fatalf("unexpected device claims: %#v", claims)
	}
	if len(result.RefreshToken) == 0 || result.RefreshExpiresAt.Before(time.Now().UTC()) {
		t.Fatalf("unexpected refresh token result %#v", result)
	}
}

func TestAuthServiceRegisterInvalidArgumentReturnsEnvelope(t *testing.T) {
	svc := newAuthService(&authRepoMock{}, &presenceCacheMock{}, nil)
	err := svc.RegisterAccount(context.Background(), iamEntity.User{}, iamEntity.UserProfile{}, "")
	if !errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Outcome != iamTaxonomy.RegisterOutcomeInvalidArgument {
		t.Fatalf("unexpected outcome: %q", appErr.Outcome)
	}
}

func TestAuthServiceLoginLoadUserErrorReturnsEnvelope(t *testing.T) {
	raw := errors.New("db timeout")
	svc := newAuthService(&authRepoMock{getUserFn: func(ctx context.Context, username string) (*iamEntity.LoginUser, error) {
		return nil, raw
	}}, nil, makeTestRegistry("secret-key"))
	_, err := svc.Login(context.Background(), iamEntity.LoginRequest{Username: "alice.nguyen", Password: "secret123", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	if !errors.Is(err, iamTaxonomy.ErrAuthenticationUnavailable) {
		t.Fatalf("expected ErrAuthenticationUnavailable, got %v", err)
	}
	if !strings.Contains(err.Error(), "db timeout") {
		t.Fatalf("expected raw cause preserved in error message, got %v", err)
	}
}
