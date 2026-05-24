package svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"controlplane/internal/config"
	"controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamErrorx "controlplane/internal/iam/errorx"
	iamSvcImpl "controlplane/internal/iam/service"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type refreshTokenRepoMock struct {
	getSessionFn func(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error)
	getUserFn    func(ctx context.Context, userID uuid.UUID) (*iamEntity.RefreshTokenUser, error)
	getDeviceFn  func(ctx context.Context, deviceID uuid.UUID) (*iamEntity.RefreshTokenDevice, error)
	revokeFn     func(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) (int64, error)
	rotateFn     func(ctx context.Context, current iamEntity.RefreshTokenSession, next iamEntity.RefreshToken) error
}

var _ iamRepoInterface.RefreshTokenRepository = (*refreshTokenRepoMock)(nil)

func (m *refreshTokenRepoMock) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error) {
	if m.getSessionFn != nil {
		return m.getSessionFn(ctx, tokenHash)
	}
	return nil, nil
}

func (m *refreshTokenRepoMock) GetRefreshTokenUserByID(ctx context.Context, userID uuid.UUID) (*iamEntity.RefreshTokenUser, error) {
	if m.getUserFn != nil {
		return m.getUserFn(ctx, userID)
	}
	return nil, nil
}

func (m *refreshTokenRepoMock) GetRefreshTokenDeviceByID(ctx context.Context, deviceID uuid.UUID) (*iamEntity.RefreshTokenDevice, error) {
	if m.getDeviceFn != nil {
		return m.getDeviceFn(ctx, deviceID)
	}
	return nil, nil
}

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
			return nil, err
		}
		session = res
	}
	if session == nil {
		return nil, nil
	}
	ctxOut := &iamEntity.RefreshContext{Session: *session}
	if m.getUserFn != nil {
		user, err := m.getUserFn(ctx, session.UserID)
		if err != nil {
			return nil, err
		}
		if user != nil {
			ctxOut.User = *user
		}
	}
	if session.DeviceID != nil && m.getDeviceFn != nil {
		dev, err := m.getDeviceFn(ctx, *session.DeviceID)
		if err != nil {
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

func (m *refreshTokenRepoMock) RotateRefreshToken(ctx context.Context, current iamEntity.RefreshTokenSession, next iamEntity.RefreshToken) error {
	if m.rotateFn != nil {
		return m.rotateFn(ctx, current, next)
	}
	return nil
}

func newRefreshTokenService(repo iamRepoInterface.RefreshTokenRepository, secrets security.SecretProvider) iamSvcInterface.RefreshTokenService {
	cfg := &config.Config{}
	cfg.Security.AccessSecretTTL = 15 * time.Minute
	cfg.Security.RefreshTokenTTL = 24 * time.Hour
	return iamSvcImpl.NewRefreshTokenService(cfg, repo, nil, secrets)
}

func TestRefreshTokenServiceInvalidSessionWhenTokenEmpty(t *testing.T) {
	svc := newRefreshTokenService(&refreshTokenRepoMock{}, &secretProviderMock{})
	_, err := svc.Refresh(context.Background(), "")
	if !errors.Is(err, iamErrorx.ErrInvalidSession) {
		t.Fatalf("expected ErrInvalidSession, got %v", err)
	}
}

func TestRefreshTokenServiceInvalidSessionWhenSessionNotFound(t *testing.T) {
	svc := newRefreshTokenService(&refreshTokenRepoMock{getSessionFn: func(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error) {
		return nil, nil
	}}, &secretProviderMock{})
	_, err := svc.Refresh(context.Background(), "raw-refresh")
	if !errors.Is(err, iamErrorx.ErrInvalidSession) {
		t.Fatalf("expected ErrInvalidSession, got %v", err)
	}
}

func TestRefreshTokenServiceNoRowsSessionMapsInvalidSession(t *testing.T) {
	svc := newRefreshTokenService(&refreshTokenRepoMock{getSessionFn: func(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error) {
		return nil, pgx.ErrNoRows
	}}, &secretProviderMock{})
	_, err := svc.Refresh(context.Background(), "raw-refresh")
	if !errors.Is(err, iamErrorx.ErrInvalidSession) {
		t.Fatalf("expected ErrInvalidSession, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Reason != iamErrorx.ReasonRefreshInvalidSession {
		t.Fatalf("unexpected reason: %q", appErr.Reason)
	}
	if !errors.Is(appErr.Cause, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows cause preserved")
	}
}

func TestRefreshTokenServiceInvalidSessionWhenExpired(t *testing.T) {
	svc := newRefreshTokenService(&refreshTokenRepoMock{getSessionFn: func(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error) {
		return &iamEntity.RefreshTokenSession{ID: uuid.New(), UserID: uuid.New(), TokenHash: tokenHash, TokenFamilyID: uuid.New(), ExpiresAt: time.Now().UTC().Add(-time.Minute)}, nil
	}}, &secretProviderMock{})
	_, err := svc.Refresh(context.Background(), "raw-refresh")
	if !errors.Is(err, iamErrorx.ErrInvalidSession) {
		t.Fatalf("expected ErrInvalidSession, got %v", err)
	}
}

func TestRefreshTokenServiceInvalidSessionWhenUserStatusBlocked(t *testing.T) {
	userID := uuid.New()
	deviceID := uuid.New()
	svc := newRefreshTokenService(&refreshTokenRepoMock{
		getSessionFn: func(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error) {
			return &iamEntity.RefreshTokenSession{ID: uuid.New(), UserID: userID, DeviceID: &deviceID, TokenHash: tokenHash, TokenFamilyID: uuid.New(), ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil
		},
		getUserFn: func(ctx context.Context, userID uuid.UUID) (*iamEntity.RefreshTokenUser, error) {
			return &iamEntity.RefreshTokenUser{ID: userID, Status: iamEntity.UserStatusDisabled}, nil
		},
	}, &secretProviderMock{})
	_, err := svc.Refresh(context.Background(), "raw-refresh")
	if !errors.Is(err, iamErrorx.ErrInvalidSession) {
		t.Fatalf("expected ErrInvalidSession, got %v", err)
	}
}

func TestRefreshTokenServiceNoRowsUserMapsInvalidSession(t *testing.T) {
	userID := uuid.New()
	deviceID := uuid.New()
	svc := newRefreshTokenService(&refreshTokenRepoMock{
		getSessionFn: func(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error) {
			return &iamEntity.RefreshTokenSession{ID: uuid.New(), UserID: userID, DeviceID: &deviceID, TokenHash: tokenHash, TokenFamilyID: uuid.New(), ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil
		},
		getUserFn: func(ctx context.Context, userID uuid.UUID) (*iamEntity.RefreshTokenUser, error) {
			return nil, pgx.ErrNoRows
		},
	}, &secretProviderMock{})
	_, err := svc.Refresh(context.Background(), "raw-refresh")
	if !errors.Is(err, iamErrorx.ErrInvalidSession) {
		t.Fatalf("expected ErrInvalidSession, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Reason != iamErrorx.ReasonRefreshInvalidSession {
		t.Fatalf("unexpected reason: %q", appErr.Reason)
	}
	if !errors.Is(appErr.Cause, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows cause preserved")
	}
}

func TestRefreshTokenServiceAuthenticationUnavailable(t *testing.T) {
	userID := uuid.New()
	deviceID := uuid.New()
	svc := newRefreshTokenService(&refreshTokenRepoMock{
		getSessionFn: func(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error) {
			return &iamEntity.RefreshTokenSession{ID: uuid.New(), UserID: userID, DeviceID: &deviceID, TokenHash: tokenHash, TokenFamilyID: uuid.New(), ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil
		},
		getUserFn: func(ctx context.Context, userID uuid.UUID) (*iamEntity.RefreshTokenUser, error) {
			return &iamEntity.RefreshTokenUser{ID: userID, Status: iamEntity.UserStatusActive}, nil
		},
		getDeviceFn: func(ctx context.Context, deviceID uuid.UUID) (*iamEntity.RefreshTokenDevice, error) {
			return &iamEntity.RefreshTokenDevice{ID: deviceID, Status: iamEntity.DeviceStatusRecognized}, nil
		},
	}, nil)
	_, err := svc.Refresh(context.Background(), "raw-refresh")
	if !errors.Is(err, iamErrorx.ErrAuthenticationUnavailable) {
		t.Fatalf("expected ErrAuthenticationUnavailable, got %v", err)
	}
}

func TestRefreshTokenServiceRotateError(t *testing.T) {
	userID := uuid.New()
	deviceID := uuid.New()
	raw := errors.New("rotate failed")
	svc := newRefreshTokenService(&refreshTokenRepoMock{
		getSessionFn: func(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error) {
			return &iamEntity.RefreshTokenSession{ID: uuid.New(), UserID: userID, DeviceID: &deviceID, TokenHash: tokenHash, TokenFamilyID: uuid.New(), ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil
		},
		getUserFn: func(ctx context.Context, userID uuid.UUID) (*iamEntity.RefreshTokenUser, error) {
			return &iamEntity.RefreshTokenUser{ID: userID, Status: iamEntity.UserStatusActive}, nil
		},
		getDeviceFn: func(ctx context.Context, deviceID uuid.UUID) (*iamEntity.RefreshTokenDevice, error) {
			return &iamEntity.RefreshTokenDevice{ID: deviceID, Status: iamEntity.DeviceStatusRecognized}, nil
		},
		rotateFn: func(ctx context.Context, current iamEntity.RefreshTokenSession, next iamEntity.RefreshToken) error {
			return raw
		},
	}, &secretProviderMock{getPrimaryFn: func(ctx context.Context, family string) (security.SecretCandidate, error) {
		return security.SecretCandidate{Family: family, Value: "secret-key", IsPrimary: true}, nil
	}})
	_, err := svc.Refresh(context.Background(), "raw-refresh")
	if !errors.Is(err, iamErrorx.ErrAuthenticationUnavailable) {
		t.Fatalf("expected ErrAuthenticationUnavailable, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Reason != iamErrorx.ReasonRefreshDependencyError {
		t.Fatalf("unexpected reason: %q", appErr.Reason)
	}
	if !errors.Is(appErr.Cause, raw) {
		t.Fatalf("expected raw cause preserved")
	}
}

func TestRefreshTokenServiceSuccess(t *testing.T) {
	userID := uuid.New()
	deviceID := uuid.New()
	familyID := uuid.New()
	rotated := false
	svc := newRefreshTokenService(&refreshTokenRepoMock{
		getSessionFn: func(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error) {
			return &iamEntity.RefreshTokenSession{ID: uuid.New(), UserID: userID, DeviceID: &deviceID, TokenHash: tokenHash, TokenFamilyID: familyID, ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil
		},
		getUserFn: func(ctx context.Context, userID uuid.UUID) (*iamEntity.RefreshTokenUser, error) {
			return &iamEntity.RefreshTokenUser{ID: userID, Status: iamEntity.UserStatusActive}, nil
		},
		getDeviceFn: func(ctx context.Context, deviceID uuid.UUID) (*iamEntity.RefreshTokenDevice, error) {
			return &iamEntity.RefreshTokenDevice{ID: deviceID, Status: iamEntity.DeviceStatusRecognized}, nil
		},
		rotateFn: func(ctx context.Context, current iamEntity.RefreshTokenSession, next iamEntity.RefreshToken) error {
			rotated = true
			if next.TokenHash == "" {
				t.Fatal("expected next token hash")
			}
			if next.TokenFamilyID != familyID {
				t.Fatalf("expected token family to be preserved, got %s", next.TokenFamilyID)
			}
			if next.DeviceID == nil || *next.DeviceID != deviceID {
				t.Fatalf("expected tracked device id to be preserved, got %#v", next.DeviceID)
			}
			return nil
		},
	}, &secretProviderMock{getPrimaryFn: func(ctx context.Context, family string) (security.SecretCandidate, error) {
		return security.SecretCandidate{Family: family, Value: "secret-key", IsPrimary: true}, nil
	}})

	result, err := svc.Refresh(context.Background(), "raw-refresh")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !rotated {
		t.Fatal("expected rotate to be called")
	}
	if result == nil || result.AccessToken == "" || result.RefreshToken == "" {
		t.Fatalf("expected refresh token result, got %#v", result)
	}
	if result.RuntimeDeviceID == "" || result.TrackedDeviceID != deviceID.String() {
		t.Fatalf("expected runtime and tracked device ids, got %#v", result)
	}
	claims, err := security.Parse(result.AccessToken, "secret-key")
	if err != nil {
		t.Fatalf("expected parsable access token, got %v", err)
	}
	if claims.DeviceID != result.RuntimeDeviceID {
		t.Fatalf("unexpected device claims: %#v", claims)
	}
}
