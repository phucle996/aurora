package svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	infraredis "controlplane/infra/redis"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamSvcImpl "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type deviceRepoMock struct {
	listFn        func(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]iamEntity.Device, error)
	getFn         func(ctx context.Context, deviceID uuid.UUID, userID uuid.UUID) (*iamEntity.Device, error)
	upsertLoginFn func(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error)
	revokeFn      func(ctx context.Context, deviceID uuid.UUID, userID uuid.UUID) error
	revokeOtherFn func(ctx context.Context, userID uuid.UUID, keepDeviceID *uuid.UUID) (int64, error)
}

func (m *deviceRepoMock) ListDevicesByUserID(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]iamEntity.Device, error) {
	return m.listFn(ctx, userID, limit, offset)
}
func (m *deviceRepoMock) GetDeviceByIDAndUserID(ctx context.Context, deviceID uuid.UUID, userID uuid.UUID) (*iamEntity.Device, error) {
	return m.getFn(ctx, deviceID, userID)
}
func (m *deviceRepoMock) UpsertLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error) {
	if m.upsertLoginFn != nil {
		return m.upsertLoginFn(ctx, device)
	}
	device.ID = uuid.NewString()
	device.Status = iamEntity.DeviceStatusRecognized
	return &device, nil
}
func (m *deviceRepoMock) RevokeDeviceByIDAndUserID(ctx context.Context, deviceID uuid.UUID, userID uuid.UUID) error {
	return m.revokeFn(ctx, deviceID, userID)
}
func (m *deviceRepoMock) RevokeOtherDevicesByUserID(ctx context.Context, userID uuid.UUID, keepDeviceID *uuid.UUID) (int64, error) {
	return m.revokeOtherFn(ctx, userID, keepDeviceID)
}
func (m *deviceRepoMock) TouchDeviceLastSeen(ctx context.Context, deviceID uuid.UUID, ip *string, userAgent *string) error {
	return nil
}
func (m *deviceRepoMock) ListUsersExceedingDeviceCap(ctx context.Context, cap int, limit int) ([]uuid.UUID, error) {
	return nil, nil
}

func (m *deviceRepoMock) EvictExcessDevices(ctx context.Context, userID uuid.UUID, cap int) ([]iamRepoInterface.EvictedDevice, error) {
	return nil, nil
}

func (m *deviceRepoMock) InsertAuditEvent(ctx context.Context, actorUserID *uuid.UUID, event string, severity string, ip *string, userAgent *string) error {
	return nil
}

type refreshRepoMock struct {
	revokeFn func(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) (int64, error)
}

func (m *refreshRepoMock) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error) {
	return nil, nil
}
func (m *refreshRepoMock) GetRefreshTokenUserByID(ctx context.Context, userID uuid.UUID) (*iamEntity.RefreshTokenUser, error) {
	return nil, nil
}
func (m *refreshRepoMock) GetRefreshTokenDeviceByID(ctx context.Context, deviceID uuid.UUID) (*iamEntity.RefreshTokenDevice, error) {
	return nil, nil
}
func (m *refreshRepoMock) RotateRefreshToken(ctx context.Context, current iamEntity.RefreshTokenSession, next iamEntity.RefreshToken) error {
	return nil
}
func (m *refreshRepoMock) RevokeRefreshTokensByUserID(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) (int64, error) {
	return m.revokeFn(ctx, userID, exceptDeviceID)
}
func (m *refreshRepoMock) LoadRefreshContextByHash(ctx context.Context, tokenHash string) (*iamEntity.RefreshContext, error) {
	return nil, nil
}

func (m *refreshRepoMock) RevokeRefreshTokensByDeviceIDsAndUserID(ctx context.Context, userID uuid.UUID, deviceIDs []uuid.UUID) (int64, error) {
	return 0, nil
}

func (m *refreshRepoMock) RevokeRefreshTokensByDeviceIDAndUserID(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (int64, error) {
	return m.revokeFn(ctx, userID, &deviceID)
}

var _ iamRepoInterface.DeviceRepository = (*deviceRepoMock)(nil)
var _ iamRepoInterface.RefreshTokenRepository = (*refreshRepoMock)(nil)

type mockStreamPublisher struct{}

func (m *mockStreamPublisher) Publish(ctx context.Context, msg infraredis.StreamMessage, idempotencyTTL time.Duration) (string, bool, error) {
	return "msg-id", true, nil
}

func newDeviceService(d iamRepoInterface.DeviceRepository, r iamRepoInterface.RefreshTokenRepository) iamSvcInterface.DeviceService {
	return iamSvcImpl.NewDeviceService(d, r, nil, &mockStreamPublisher{})
}

func TestDeviceServiceListMyDevicesInvalidUserID(t *testing.T) {
	svc := newDeviceService(&deviceRepoMock{}, &refreshRepoMock{})
	_, err := svc.ListMyDevices(context.Background(), "not-uuid", 10, 0)
	if !errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
}

func TestDeviceServiceRevokeMyDeviceNotOwned(t *testing.T) {
	svc := newDeviceService(&deviceRepoMock{
		getFn: func(ctx context.Context, deviceID uuid.UUID, userID uuid.UUID) (*iamEntity.Device, error) {
			return nil, pgx.ErrNoRows
		},
		revokeFn:      func(ctx context.Context, deviceID uuid.UUID, userID uuid.UUID) error { return nil },
		revokeOtherFn: func(ctx context.Context, userID uuid.UUID, keepDeviceID *uuid.UUID) (int64, error) { return 0, nil },
	}, &refreshRepoMock{
		revokeFn: func(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) (int64, error) { return 0, nil },
	})
	err := svc.RevokeMyDevice(context.Background(), uuid.NewString(), uuid.NewString(), nil, nil)
	if !errors.Is(err, iamTaxonomy.ErrInvalidSession) {
		t.Fatalf("expected ErrInvalidSession, got %v", err)
	}
}

func TestDeviceServiceLogoutOtherDevicesSuccess(t *testing.T) {
	var revokedTokenN int64 = 2
	svc := newDeviceService(&deviceRepoMock{
		listFn: func(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]iamEntity.Device, error) {
			return nil, nil
		},
		getFn: func(ctx context.Context, deviceID uuid.UUID, userID uuid.UUID) (*iamEntity.Device, error) {
			return nil, nil
		},
		revokeFn: func(ctx context.Context, deviceID uuid.UUID, userID uuid.UUID) error { return nil },
		revokeOtherFn: func(ctx context.Context, userID uuid.UUID, keepDeviceID *uuid.UUID) (int64, error) {
			return 3, nil
		},
	}, &refreshRepoMock{
		revokeFn: func(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) (int64, error) {
			return revokedTokenN, nil
		},
	})
	n, err := svc.LogoutOtherDevices(context.Background(), uuid.NewString(), uuid.NewString(), nil, nil)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if n != revokedTokenN {
		t.Fatalf("expected revoked token %d, got %d", revokedTokenN, n)
	}
}
