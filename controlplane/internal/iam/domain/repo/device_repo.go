package iamRepoInterface

import (
	"context"

	"controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

type DeviceRepository interface {
	UpsertLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error)
	ListDevicesByUserID(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]iamEntity.Device, error)
	GetDeviceByIDAndUserID(ctx context.Context, deviceID uuid.UUID, userID uuid.UUID) (*iamEntity.Device, error)
	RevokeDeviceByIDAndUserID(ctx context.Context, deviceID uuid.UUID, userID uuid.UUID) error
	RevokeOtherDevicesByUserID(ctx context.Context, userID uuid.UUID, keepDeviceID *uuid.UUID) (int64, error)
	TouchDeviceLastSeen(ctx context.Context, deviceID uuid.UUID, ip *string, userAgent *string) error
	InsertAuditEvent(ctx context.Context, actorUserID *uuid.UUID, event string, severity string, ip *string, userAgent *string) error
	EvictExcessDevices(ctx context.Context, userID uuid.UUID, cap int) ([]EvictedDevice, error)
	ListUsersExceedingDeviceCap(ctx context.Context, cap int, limit int) ([]uuid.UUID, error)
}

// EvictedDevice là output của EvictExcessDevices.
type EvictedDevice struct {
	DeviceID       uuid.UUID
	ClientDeviceID *string
}

