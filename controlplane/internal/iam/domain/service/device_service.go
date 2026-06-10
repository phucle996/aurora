package iamSvcInterface

import (
	"context"
	"time"

	"controlplane/internal/iam/domain/entity"
	"github.com/google/uuid"
)

type DevicePresence struct {
	Device     iamEntity.Device
	IsOnline   bool
	LastSeenAt *time.Time
	LastIP     *string
	LastUA     *string
}

type DeviceListResult struct {
	Devices []DevicePresence
	Total   int64
}

type DeviceService interface {
	ListMyDevices(ctx context.Context, userID string, limit int, offset int) (*DeviceListResult, error)
	RevokeMyDevice(ctx context.Context, userID string, deviceID string, ip *string, userAgent *string) error
	LogoutOtherDevices(ctx context.Context, userID string, currentTrackedDeviceID string, ip *string, userAgent *string) (int64, error)
	LogoutAllDevices(ctx context.Context, userID string, ip *string, userAgent *string) (int64, error)

	RegisterLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error)
	TouchDeviceLastSeen(ctx context.Context, deviceID uuid.UUID, ip *string, userAgent *string) error
	EvictExcessDevicesIfNeeded(ctx context.Context, userID uuid.UUID, ip *string, userAgent *string)
	ReconcileDeviceCap(ctx context.Context, batch int) (int, error)
	PublishDeviceAuditAsync(ctx context.Context, userID uuid.UUID, event string, severity string, ip *string, userAgent *string, extras map[string]string)
}
