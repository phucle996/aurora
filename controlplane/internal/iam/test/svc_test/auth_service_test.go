package svc_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	coreEntity "controlplane/internal/hierarchy/domain/entity"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamSvcImpl "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/iam/test/testutil"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"
	"controlplane/pkg/id"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

type authRepoMock struct {
	createFn        func(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error
	getUserFn       func(ctx context.Context, username string) (*iamEntity.LoginUser, error)
	createRefreshFn func(ctx context.Context, token iamEntity.RefreshToken) error
}

var _ iamRepoInterface.AuthRepository = (*authRepoMock)(nil)

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

func makeTestRegistry(secretKey string, rdb *redis.Client) *cacheengine.CacheRegistry {
	security.SetRuntimeMasterKey(make([]byte, 32))
	l1Cache := cacheengine.NewShardedCache()
	registry := cacheengine.NewCacheRegistry(l1Cache)
	if rdb == nil {
		if mr, err := miniredis.Run(); err == nil {
			rdb = redis.NewClient(&redis.Options{Addr: mr.Addr()})
		} else {
			rdb = redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
		}
	}
	registry.L2 = cacheengine.NewL2Cache(rdb)
	registry.Exec = cacheengine.NewL2LuaExecutor(rdb)
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
	cacheengine.Register(registry, "zone_by_code", 5*time.Minute, func(ctx context.Context, param string) (string, error) {
		return "00000000-0000-0000-0000-000000000000", nil
	})
	return registry
}

type deviceServiceStub struct {
	registerLoginDeviceFn func(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error)
	getActiveDeviceIDFn   func(ctx context.Context, userID uuid.UUID, devicePublicKey string) (string, error)
}

var _ iamSvcInterface.DeviceService = (*deviceServiceStub)(nil)

func (s *deviceServiceStub) ListMyDevices(ctx context.Context, userID uuid.UUID, limit int, offset int) (*iamEntity.DeviceListResult, error) {
	return nil, nil
}
func (s *deviceServiceStub) RevokeMyDevice(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID, currentDeviceID uuid.UUID) error {
	return nil
}
func (s *deviceServiceStub) LogoutOtherDevices(ctx context.Context, userID uuid.UUID, currentTrackedDeviceID *uuid.UUID) (int64, error) {
	return 0, nil
}
func (s *deviceServiceStub) LogoutAllDevices(ctx context.Context, userID uuid.UUID) (int64, error) {
	return 0, nil
}
func (s *deviceServiceStub) RegisterLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error) {
	if s.registerLoginDeviceFn != nil {
		return s.registerLoginDeviceFn(ctx, device)
	}
	device.ID = uuid.NewString()
	return &device, nil
}
func (s *deviceServiceStub) TouchDeviceLastSeen(ctx context.Context, deviceID uuid.UUID) error {
	return nil
}
func (s *deviceServiceStub) EvictExcessDevicesIfNeeded(ctx context.Context, userID uuid.UUID) {
}
func (s *deviceServiceStub) ReconcileDeviceCap(ctx context.Context, batch int) (int, error) {
	return 0, nil
}
func (s *deviceServiceStub) PublishDeviceAuditAsync(ctx context.Context, userID uuid.UUID, event string, severity string, extras map[string]string) {
}
func (s *deviceServiceStub) GetActiveDeviceID(ctx context.Context, userID uuid.UUID, devicePublicKey string) (string, error) {
	if s.getActiveDeviceIDFn != nil {
		return s.getActiveDeviceIDFn(ctx, userID, devicePublicKey)
	}
	return "", nil
}

type sessionRefreshServiceStub struct {
	createRefreshTokenFn func(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (string, time.Time, error)
}

var _ iamSvcInterface.SessionRefreshService = (*sessionRefreshServiceStub)(nil)

func (s *sessionRefreshServiceStub) CreateRefreshToken(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (string, time.Time, error) {
	if s.createRefreshTokenFn != nil {
		return s.createRefreshTokenFn(ctx, userID, deviceID)
	}
	return "mock-refresh-token", time.Now().UTC().Add(24 * time.Hour), nil
}

func (s *sessionRefreshServiceStub) VerifyOpaqueRefreshToken(ctx context.Context, rawRefreshToken string, scope string) (*iamEntity.VerifyOpaqueRefreshTokenResult, error) {
	return nil, nil
}

func (s *sessionRefreshServiceStub) RefreshAdminTrinity(ctx context.Context, zoneCode string, ip *string, userAgent *string) (iamEntity.AdminLoginResult, error) {
	return iamEntity.AdminLoginResult{}, nil
}

// [COMMENT]: Thêm stub RevokeOpaqueRefreshToken để thoả mãn interface mới
func (s *sessionRefreshServiceStub) RevokeOpaqueRefreshToken(ctx context.Context, rawRefreshToken string) error {
	return nil
}

func newAuthService(repo iamRepoInterface.AuthRepository, registry *cacheengine.CacheRegistry) iamSvcInterface.AuthService {
	refreshStub := &sessionRefreshServiceStub{
		createRefreshTokenFn: func(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (string, time.Time, error) {
			token := iamEntity.RefreshToken{
				ID:        uuid.New(),
				UserID:    userID,
				DeviceID:  &deviceID,
				TokenHash: "mock-hash",
				IssuedAt:  time.Now().UTC(),
				ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
			}
			var err error
			if mock, ok := repo.(*authRepoMock); ok && mock.createRefreshFn != nil {
				err = mock.createRefreshFn(ctx, token)
			}
			return "mock-refresh-token", token.ExpiresAt, err
		},
	}
	return iamSvcImpl.NewAuthService(config.LoadConfig(), repo, &rbacRepoMock{}, refreshStub, &deviceServiceStub{}, registry, nil, nil, &testutil.SessionServiceClientMock{})
}

func TestAuthServiceRegisterAccountSuccessOnBitmapMiss(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	registry := makeTestRegistry("secret-key", rdb)

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
	}}, registry)

	err := svc.RegisterAccount(context.Background(), iamEntity.User{Username: "alice.nguyen", Email: "user@example.com"}, iamEntity.UserProfile{Fullname: "Alice Nguyen"}, "secret123")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !created {
		t.Fatal("expected repository create to be called")
	}

	usernameDigest, _ := security.PresenceHMACSHA256Hex("iam.register.username", "alice.nguyen")
	emailDigest, _ := security.PresenceHMACSHA256Hex("iam.register.email", "user@example.com")
	usernameBit, _ := rdb.GetBit(context.Background(), "iam:register:bitmap:username", id.BitmapIndex(usernameDigest)).Result()
	emailBit, _ := rdb.GetBit(context.Background(), "iam:register:bitmap:email", id.BitmapIndex(emailDigest)).Result()
	if usernameBit != 1 || emailBit != 1 {
		t.Fatal("expected presence cache bits to be set in Redis")
	}
}

func TestAuthServiceRegisterAccountPresenceCheckErrorFallsBackToDB(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	registry := makeTestRegistry("secret-key", rdb)

	created := false
	svc := newAuthService(&authRepoMock{createFn: func(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error {
		created = true
		return nil
	}}, registry)

	err := svc.RegisterAccount(context.Background(), iamEntity.User{Username: "alice.nguyen", Email: "user@example.com"}, iamEntity.UserProfile{Fullname: "Alice Nguyen"}, "secret123")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !created {
		t.Fatal("expected repository create to be called")
	}
}

func TestAuthServiceRegisterAccountBitmapHitAndUserExists(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	registry := makeTestRegistry("secret-key", rdb)

	usernameDigest, _ := security.PresenceHMACSHA256Hex("iam.register.username", "alice.nguyen")
	rdb.SetBit(context.Background(), "iam:register:bitmap:username", id.BitmapIndex(usernameDigest), 1)

	created := false
	svc := newAuthService(&authRepoMock{
		createFn: func(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error {
			created = true
			return iamTaxonomy.ErrUserAlreadyExist
		},
	}, registry)

	err := svc.RegisterAccount(context.Background(), iamEntity.User{Username: "alice.nguyen", Email: "user@example.com"}, iamEntity.UserProfile{Fullname: "Alice Nguyen"}, "secret123")
	if !errors.Is(err, iamTaxonomy.ErrUserAlreadyExist) {
		t.Fatalf("expected ErrUserAlreadyExist, got %v", err)
	}
	if !created {
		t.Fatal("expected repository create to be called")
	}
}

func TestAuthServiceRegisterAccountBitmapHitFalsePositiveThenInsert(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	registry := makeTestRegistry("secret-key", rdb)

	emailDigest, _ := security.PresenceHMACSHA256Hex("iam.register.email", "user@example.com")
	rdb.SetBit(context.Background(), "iam:register:bitmap:email", id.BitmapIndex(emailDigest), 1)

	created := false
	svc := newAuthService(&authRepoMock{
		createFn: func(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error {
			created = true
			return nil
		},
	}, registry)

	err := svc.RegisterAccount(context.Background(), iamEntity.User{Username: "alice.nguyen", Email: "user@example.com"}, iamEntity.UserProfile{Fullname: "Alice Nguyen"}, "secret123")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !created {
		t.Fatalf("expected created true")
	}

	usernameDigest, _ := security.PresenceHMACSHA256Hex("iam.register.username", "alice.nguyen")
	usernameBit, _ := rdb.GetBit(context.Background(), "iam:register:bitmap:username", id.BitmapIndex(usernameDigest)).Result()
	if usernameBit != 1 {
		t.Fatal("expected presence key for username to be set in Redis")
	}
}

func TestAuthServiceRegisterAccountDuplicateFromRepoMarksCache(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	registry := makeTestRegistry("secret-key", rdb)

	svc := newAuthService(&authRepoMock{createFn: func(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error {
		return iamTaxonomy.ErrUserAlreadyExist
	}}, registry)

	err := svc.RegisterAccount(context.Background(), iamEntity.User{Username: "alice.nguyen", Email: "user@example.com"}, iamEntity.UserProfile{Fullname: "Alice Nguyen"}, "secret123")
	if !errors.Is(err, iamTaxonomy.ErrUserAlreadyExist) {
		t.Fatalf("expected ErrUserAlreadyExist, got %v", err)
	}

	usernameDigest, _ := security.PresenceHMACSHA256Hex("iam.register.username", "alice.nguyen")
	emailDigest, _ := security.PresenceHMACSHA256Hex("iam.register.email", "user@example.com")
	usernameBit, _ := rdb.GetBit(context.Background(), "iam:register:bitmap:username", id.BitmapIndex(usernameDigest)).Result()
	emailBit, _ := rdb.GetBit(context.Background(), "iam:register:bitmap:email", id.BitmapIndex(emailDigest)).Result()
	if usernameBit != 1 || emailBit != 1 {
		t.Fatal("expected presence cache to be marked on duplicate")
	}
}

func TestAuthServiceRegisterAccountDuplicateStillReturnsDuplicateWhenMarkFails(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	registry := makeTestRegistry("secret-key", rdb)

	svc := newAuthService(&authRepoMock{createFn: func(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error {
		return iamTaxonomy.ErrUserAlreadyExist
	}}, registry)

	err := svc.RegisterAccount(context.Background(), iamEntity.User{Username: "alice.nguyen", Email: "user@example.com"}, iamEntity.UserProfile{Fullname: "Alice Nguyen"}, "secret123")
	if !errors.Is(err, iamTaxonomy.ErrUserAlreadyExist) {
		t.Fatalf("expected ErrUserAlreadyExist, got %v", err)
	}
}

func TestAuthServiceRegisterAccountSuccessStillReturnsSuccessWhenMarkFails(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	registry := makeTestRegistry("secret-key", rdb)

	created := false
	svc := newAuthService(&authRepoMock{createFn: func(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error {
		created = true
		return nil
	}}, registry)

	err := svc.RegisterAccount(context.Background(), iamEntity.User{Username: "alice.nguyen", Email: "user@example.com"}, iamEntity.UserProfile{Fullname: "Alice Nguyen"}, "secret123")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !created {
		t.Fatal("expected repository create to be called")
	}
}

func TestAuthServiceVerifyUserCredentialsUserNotFound(t *testing.T) {
	svc := newAuthService(&authRepoMock{getUserFn: func(ctx context.Context, username string) (*iamEntity.LoginUser, error) {
		return nil, iamTaxonomy.ErrInvalidCredentials
	}}, nil)
	_, err := svc.VerifyUserCredentials(context.Background(), iamEntity.LoginRequest{Username: "alice.nguyen", Password: "secret123", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	if !errors.Is(err, iamTaxonomy.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestAuthServiceVerifyUserCredentialsNoRowsErrorMapsInvalidCredentials(t *testing.T) {
	svc := newAuthService(&authRepoMock{getUserFn: func(ctx context.Context, username string) (*iamEntity.LoginUser, error) {
		return nil, fmt.Errorf("%w: %w", iamTaxonomy.ErrInvalidCredentials, pgx.ErrNoRows)
	}}, nil)
	_, err := svc.VerifyUserCredentials(context.Background(), iamEntity.LoginRequest{Username: "alice.nguyen", Password: "secret123", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	if !errors.Is(err, iamTaxonomy.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Outcome != "invalid_credential" {
		t.Fatalf("unexpected outcome: %q", appErr.Outcome)
	}
	if !errors.Is(appErr.Cause, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows cause preserved")
	}
}

func TestAuthServiceVerifyUserCredentialsWrongPassword(t *testing.T) {
	hash, _ := security.HashPassword("secret123")
	svc := newAuthService(&authRepoMock{getUserFn: func(ctx context.Context, username string) (*iamEntity.LoginUser, error) {
		return &iamEntity.LoginUser{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Username: username, PasswordHash: hash, Status: iamEntity.UserStatusActive}, nil
	}}, makeTestRegistry("secret-key", nil))
	_, err := svc.VerifyUserCredentials(context.Background(), iamEntity.LoginRequest{Username: "alice.nguyen", Password: "wrongpass", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	if !errors.Is(err, iamTaxonomy.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestAuthServiceVerifyUserCredentialsPendingActiveBlocked(t *testing.T) {
	hash, _ := security.HashPassword("secret123")
	svc := newAuthService(&authRepoMock{getUserFn: func(ctx context.Context, username string) (*iamEntity.LoginUser, error) {
		return &iamEntity.LoginUser{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Username: username, PasswordHash: hash, Status: iamEntity.UserStatusPendingActive}, nil
	}}, makeTestRegistry("secret-key", nil))
	_, err := svc.VerifyUserCredentials(context.Background(), iamEntity.LoginRequest{Username: "alice.nguyen", Password: "secret123", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	if !errors.Is(err, iamTaxonomy.ErrVerificationRequired) {
		t.Fatalf("expected ErrVerificationRequired, got %v", err)
	}
}

func TestAuthServiceVerifyUserCredentialsSuccess(t *testing.T) {
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
	}, makeTestRegistry("secret-key", nil))

	res, err := svc.VerifyUserCredentials(context.Background(), iamEntity.LoginRequest{Username: "alice.nguyen", Password: "secret123", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", TrustDevice: true})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !res.Valid {
		t.Fatal("expected result to be valid")
	}
	if persisted == false {
		t.Fatal("expected refresh session to be persisted")
	}
}

func TestAuthServiceVerifyUserCredentialsLoadUserErrorReturnsEnvelope(t *testing.T) {
	raw := errors.New("db timeout")
	svc := newAuthService(&authRepoMock{getUserFn: func(ctx context.Context, username string) (*iamEntity.LoginUser, error) {
		return nil, raw
	}}, makeTestRegistry("secret-key", nil))
	_, err := svc.VerifyUserCredentials(context.Background(), iamEntity.LoginRequest{Username: "alice.nguyen", Password: "secret123", DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	if !errors.Is(err, iamTaxonomy.ErrAuthenticationUnavailable) {
		t.Fatalf("expected ErrAuthenticationUnavailable, got %v", err)
	}
	if !strings.Contains(err.Error(), "db timeout") {
		t.Fatalf("expected raw cause preserved in error message, got %v", err)
	}
}

func TestAuthServiceVerifyUserCredentialsRevokedDeviceHeals(t *testing.T) {
	hash, _ := security.HashPassword("secret123")
	user := &iamEntity.LoginUser{
		ID:           uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Username:     "alice.nguyen",
		PasswordHash: hash,
		Status:       iamEntity.UserStatusActive,
	}

	repo := &authRepoMock{
		getUserFn: func(ctx context.Context, username string) (*iamEntity.LoginUser, error) {
			return user, nil
		},
		createRefreshFn: func(ctx context.Context, token iamEntity.RefreshToken) error {
			return nil
		},
	}

	revokedDeviceID := uuid.MustParse("99999999-9999-9999-9999-999999999999")

	var registeredDevice iamEntity.Device
	// Setup custom deviceSvc stub
	devSvc := &deviceServiceStub{
		getActiveDeviceIDFn: func(ctx context.Context, userID uuid.UUID, devicePublicKey string) (string, error) {
			// [COMMENT]: Trả về chuỗi rỗng để mô phỏng không có thiết bị active nào (do thiết bị đã bị thu hồi hoặc chưa đăng ký).
			return "", nil
		},
		registerLoginDeviceFn: func(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error) {
			// [COMMENT]: Đăng ký thiết bị mới cần trả về trạng thái hoạt động bình thường
			device.ID = uuid.NewString()
			device.Status = iamEntity.DeviceStatusRecognized
			registeredDevice = device
			return &device, nil
		},
	}

	refreshStub := &sessionRefreshServiceStub{
		createRefreshTokenFn: func(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (string, time.Time, error) {
			return "mock-refresh-token", time.Now().UTC().Add(24 * time.Hour), nil
		},
	}

	// [COMMENT]: Khởi tạo AuthService thủ công để tiêm (inject) mock devSvc tùy biến phục vụ test tự hồi phục cookie thiết bị.
	svc := iamSvcImpl.NewAuthService(config.LoadConfig(), repo, &rbacRepoMock{}, refreshStub, devSvc, makeTestRegistry("secret-key", nil), nil, nil, &testutil.SessionServiceClientMock{})

	res, err := svc.VerifyUserCredentials(context.Background(), iamEntity.LoginRequest{
		Username:        "alice.nguyen",
		Password:        "secret123",
		DevicePublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		ClientDeviceID:  revokedDeviceID,
		TrustDevice:     true,
	})

	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !res.Valid {
		t.Fatal("expected result to be valid")
	}

	// [COMMENT]: Khẳng định ClientDeviceID mới sinh ra phải khác ClientDeviceID cũ bị revoked
	if registeredDevice.ClientDeviceID == nil || *registeredDevice.ClientDeviceID == revokedDeviceID.String() {
		t.Fatalf("expected revoked client device ID to be discarded and healed")
	}
}
