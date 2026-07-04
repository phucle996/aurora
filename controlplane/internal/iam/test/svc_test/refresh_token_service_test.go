package svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamSvcImpl "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type refreshTokenRepoMock struct {
	getSessionFn func(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error)
	getUserFn    func(ctx context.Context, userID uuid.UUID) (*iamEntity.RefreshTokenUser, error)
	getDeviceFn  func(ctx context.Context, deviceID uuid.UUID) (*iamEntity.RefreshTokenDevice, error)
	revokeFn     func(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) (int64, error)
}

var _ iamRepoInterface.RefreshTokenRepository = (*refreshTokenRepoMock)(nil)

func (m *refreshTokenRepoMock) RevokeRefreshTokensByUserID(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) (int64, error) {
	if m.revokeFn != nil {
		return m.revokeFn(ctx, userID, exceptDeviceID)
	}
	return 0, nil
}
func (m *refreshTokenRepoMock) LoadRefreshContextByHash(ctx context.Context, tokenHash string) (*iamEntity.RefreshContext, error) {
	var session *iamEntity.RefreshTokenSession
	if m.getSessionFn != nil {
		res, err := m.getSessionFn(ctx, tokenHash)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, iamTaxonomy.ErrNotFound
			}
			return nil, err
		}
		session = res
	}
	if session == nil {
		return nil, iamTaxonomy.ErrNotFound
	}
	ctxOut := &iamEntity.RefreshContext{Session: *session}
	if m.getUserFn != nil {
		user, err := m.getUserFn(ctx, session.UserID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, iamTaxonomy.ErrNotFound
			}
			return nil, err
		}
		if user != nil {
			ctxOut.User = *user
		}
	}
	if session.DeviceID != nil && m.getDeviceFn != nil {
		dev, err := m.getDeviceFn(ctx, *session.DeviceID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, iamTaxonomy.ErrNotFound
			}
			return nil, err
		}
		ctxOut.Device = dev
	}
	return ctxOut, nil
}

func (m *refreshTokenRepoMock) RevokeRefreshTokensByDeviceIDsAndUserID(ctx context.Context, userID uuid.UUID, deviceIDs []uuid.UUID) (int64, error) {
	return 0, nil
}

func (m *refreshTokenRepoMock) RevokeRefreshTokensByDeviceIDAndUserID(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (int64, error) {
	if m.revokeFn != nil {
		return m.revokeFn(ctx, userID, &deviceID)
	}
	return 0, nil
}

func (m *refreshTokenRepoMock) CreateRefreshTokenSession(ctx context.Context, token iamEntity.RefreshToken) error {
	return nil
}

func (m *refreshTokenRepoMock) DeleteRefreshTokenSessionByHash(ctx context.Context, tokenHash string) (int64, error) {
	return 0, nil
}

type refreshRbacRepoMock struct {
	iamRepoInterface.RbacRepository
	getRoleIDByUserIDFn func(ctx context.Context, userID uuid.UUID) (string, int32, error)
}

func (m *refreshRbacRepoMock) GetRoleIDByUserID(ctx context.Context, userID uuid.UUID) (string, int32, error) {
	if m.getRoleIDByUserIDFn != nil {
		return m.getRoleIDByUserIDFn(ctx, userID)
	}
	return "user-role-id", 1, nil
}

func newRefreshTokenService(repo iamRepoInterface.RefreshTokenRepository, rbacRepo iamRepoInterface.RbacRepository, registry *cacheengine.CacheRegistry) iamSvcInterface.SessionRefreshService {
	cfg := &config.Config{}
	cfg.Security.RefreshTokenTTL = 24 * time.Hour
	return iamSvcImpl.NewSessionRefreshService(cfg, repo, rbacRepo, registry)
}

func TestRefreshTokenServiceInvalidSessionWhenSessionNotFound(t *testing.T) {
	svc := newRefreshTokenService(&refreshTokenRepoMock{getSessionFn: func(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error) {
		return nil, nil
	}}, nil, nil)
	res, err := svc.VerifyOpaqueRefreshToken(context.Background(), "raw-refresh", nil, uuid.New())
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if res.Valid {
		t.Fatal("expected invalid token result")
	}
}

func TestRefreshTokenServiceNoRowsSessionMapsInvalidSession(t *testing.T) {
	svc := newRefreshTokenService(&refreshTokenRepoMock{getSessionFn: func(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error) {
		return nil, iamTaxonomy.ErrNotFound
	}}, nil, nil)
	res, err := svc.VerifyOpaqueRefreshToken(context.Background(), "raw-refresh", nil, uuid.New())
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if res.Valid {
		t.Fatal("expected invalid token result")
	}
}

func TestRefreshTokenServiceDatabaseError(t *testing.T) {
	dbErr := errors.New("db error")
	svc := newRefreshTokenService(&refreshTokenRepoMock{getSessionFn: func(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error) {
		return nil, dbErr
	}}, nil, nil)
	_, err := svc.VerifyOpaqueRefreshToken(context.Background(), "raw-refresh", nil, uuid.New())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRefreshTokenServiceInvalidSessionWhenExpired(t *testing.T) {
	svc := newRefreshTokenService(&refreshTokenRepoMock{getSessionFn: func(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error) {
		return &iamEntity.RefreshTokenSession{ID: uuid.New(), UserID: uuid.New(), TokenHash: tokenHash, ExpiresAt: time.Now().UTC().Add(-time.Minute)}, nil
	}}, nil, nil)
	res, err := svc.VerifyOpaqueRefreshToken(context.Background(), "raw-refresh", nil, uuid.New())
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if res.Valid {
		t.Fatal("expected invalid token result")
	}
}

func TestRefreshTokenServiceInvalidSessionWhenUserStatusBlocked(t *testing.T) {
	userID := uuid.New()
	deviceID := uuid.New()
	svc := newRefreshTokenService(&refreshTokenRepoMock{
		getSessionFn: func(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error) {
			return &iamEntity.RefreshTokenSession{ID: uuid.New(), UserID: userID, DeviceID: &deviceID, TokenHash: tokenHash, ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil
		},
		getUserFn: func(ctx context.Context, userID uuid.UUID) (*iamEntity.RefreshTokenUser, error) {
			return &iamEntity.RefreshTokenUser{ID: userID, Status: iamEntity.UserStatusDisabled}, nil
		},
	}, nil, nil)
	res, err := svc.VerifyOpaqueRefreshToken(context.Background(), "raw-refresh", nil, uuid.New())
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if res.Valid {
		t.Fatal("expected invalid token result")
	}
}

func TestRefreshTokenServiceNoRowsUserMapsInvalidSession(t *testing.T) {
	userID := uuid.New()
	deviceID := uuid.New()
	svc := newRefreshTokenService(&refreshTokenRepoMock{
		getSessionFn: func(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error) {
			return &iamEntity.RefreshTokenSession{ID: uuid.New(), UserID: userID, DeviceID: &deviceID, TokenHash: tokenHash, ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil
		},
		getUserFn: func(ctx context.Context, userID uuid.UUID) (*iamEntity.RefreshTokenUser, error) {
			return nil, pgx.ErrNoRows
		},
	}, nil, nil)
	res, err := svc.VerifyOpaqueRefreshToken(context.Background(), "raw-refresh", nil, uuid.New())
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if res.Valid {
		t.Fatal("expected invalid token result")
	}
}

func TestRefreshTokenServiceSuccess(t *testing.T) {
	userID := uuid.New()
	deviceID := uuid.New()
	svc := newRefreshTokenService(&refreshTokenRepoMock{
		getSessionFn: func(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error) {
			return &iamEntity.RefreshTokenSession{ID: uuid.New(), UserID: userID, DeviceID: &deviceID, TokenHash: tokenHash, ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil
		},
		getUserFn: func(ctx context.Context, userID uuid.UUID) (*iamEntity.RefreshTokenUser, error) {
			return &iamEntity.RefreshTokenUser{ID: userID, Status: iamEntity.UserStatusActive}, nil
		},
		getDeviceFn: func(ctx context.Context, deviceID uuid.UUID) (*iamEntity.RefreshTokenDevice, error) {
			return &iamEntity.RefreshTokenDevice{ID: deviceID, Status: iamEntity.DeviceStatusRecognized}, nil
		},
	}, &refreshRbacRepoMock{
		getRoleIDByUserIDFn: func(ctx context.Context, uID uuid.UUID) (string, int32, error) {
			return "admin-role-uuid", 99, nil
		},
	}, nil)

	res, err := svc.VerifyOpaqueRefreshToken(context.Background(), "raw-refresh", nil, userID)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !res.Valid {
		t.Fatal("expected valid token result")
	}
	if res.UserID != userID.String() {
		t.Fatalf("expected user ID %s, got %s", userID.String(), res.UserID)
	}
	if res.RoleID != "admin-role-uuid" || res.Level != 99 {
		t.Fatalf("unexpected role or level: %s, %d", res.RoleID, res.Level)
	}
}
