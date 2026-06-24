package svc_test

import (
	"context"
	"errors"
	"testing"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamSvcImpl "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/pkg/constant"

	"github.com/google/uuid"
)

type deviceRepoMock struct {
	listFn              func(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]iamEntity.Device, error)
	upsertLoginFn       func(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error)
	revokeFn            func(ctx context.Context, deviceID uuid.UUID, userID uuid.UUID) error
	revokeOtherFn       func(ctx context.Context, userID uuid.UUID, keepDeviceID *uuid.UUID) (int64, error)
	getActiveDeviceIDFn func(ctx context.Context, userID uuid.UUID, fingerprint string) (string, error)
}

func (m *deviceRepoMock) ListDevicesByUserID(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]iamEntity.Device, error) {
	return m.listFn(ctx, userID, limit, offset)
}
func (m *deviceRepoMock) GetActiveDeviceID(ctx context.Context, userID uuid.UUID, fingerprint string) (string, error) {
	if m.getActiveDeviceIDFn != nil {
		return m.getActiveDeviceIDFn(ctx, userID, fingerprint)
	}
	return "", nil
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
func (m *deviceRepoMock) TouchDeviceLastSeen(ctx context.Context, deviceID uuid.UUID) error {
	return nil
}
func (m *deviceRepoMock) ListUsersExceedingDeviceCap(ctx context.Context, cap int, limit int) ([]uuid.UUID, error) {
	return nil, nil
}

func (m *deviceRepoMock) EvictExcessDevices(ctx context.Context, userID uuid.UUID, cap int) ([]iamRepoInterface.EvictedDevice, error) {
	return nil, nil
}

func (m *deviceRepoMock) InsertAuditEvent(ctx context.Context, actorUserID *uuid.UUID, event string, severity string) error {
	return nil
}

type refreshRepoMock struct {
	revokeFn func(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) (int64, error)
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

func (m *refreshRepoMock) CreateRefreshTokenSession(ctx context.Context, token iamEntity.RefreshToken) error {
	return nil
}

func (m *refreshRepoMock) RevokeRefreshTokensByDeviceIDAndUserID(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (int64, error) {
	return m.revokeFn(ctx, userID, &deviceID)
}

// [COMMENT]: Thêm mock DeleteRefreshTokenSessionByHash để thoả mãn interface mới
func (m *refreshRepoMock) DeleteRefreshTokenSessionByHash(ctx context.Context, tokenHash string) (int64, error) {
	return 0, nil
}

var _ iamRepoInterface.DeviceRepository = (*deviceRepoMock)(nil)
var _ iamRepoInterface.RefreshTokenRepository = (*refreshRepoMock)(nil)

func newDeviceService(d iamRepoInterface.DeviceRepository, r iamRepoInterface.RefreshTokenRepository) iamSvcInterface.DeviceService {
	return iamSvcImpl.NewDeviceService(d, r, nil)
}

func TestDeviceServiceRevokeMyDeviceNotOwned(t *testing.T) {
	svc := newDeviceService(&deviceRepoMock{
		revokeFn: func(ctx context.Context, deviceID uuid.UUID, userID uuid.UUID) error {
			return iamTaxonomy.ErrZeroRowsAffected
		},
		revokeOtherFn: func(ctx context.Context, userID uuid.UUID, keepDeviceID *uuid.UUID) (int64, error) { return 0, nil },
	}, &refreshRepoMock{
		revokeFn: func(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) (int64, error) { return 0, nil },
	})
	userID := uuid.New()
	ident := &constant.Identity{UserID: userID.String()}
	ctx := context.WithValue(context.Background(), constant.IdentityKey, ident)
	err := svc.RevokeMyDevice(ctx, uuid.New())
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
		revokeFn: func(ctx context.Context, deviceID uuid.UUID, userID uuid.UUID) error { return nil },
		revokeOtherFn: func(ctx context.Context, userID uuid.UUID, keepDeviceID *uuid.UUID) (int64, error) {
			return 3, nil
		},
	}, &refreshRepoMock{
		revokeFn: func(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) (int64, error) {
			return revokedTokenN, nil
		},
	})
	userID := uuid.New()
	ident := &constant.Identity{UserID: userID.String()}
	ctx := context.WithValue(context.Background(), constant.IdentityKey, ident)
	keep := uuid.New()
	n, err := svc.LogoutOtherDevices(ctx, &keep)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if n != revokedTokenN {
		t.Fatalf("expected revoked token %d, got %d", revokedTokenN, n)
	}
}
