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
	// [COMMENT]: Thay đổi chữ ký hàm để truyền userID trực tiếp từ handler thay vì giải mã trong context.
	ListMyDevices(ctx context.Context, userID uuid.UUID, limit int, offset int) (*DeviceListResult, error)
	RevokeMyDevice(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID, currentDeviceID uuid.UUID) error
	LogoutOtherDevices(ctx context.Context, userID uuid.UUID, currentTrackedDeviceID *uuid.UUID) (int64, error)
	LogoutAllDevices(ctx context.Context, userID uuid.UUID) (int64, error)

	RegisterLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error)
	TouchDeviceLastSeen(ctx context.Context, deviceID uuid.UUID) error
	EvictExcessDevicesIfNeeded(ctx context.Context, userID uuid.UUID)
	ReconcileDeviceCap(ctx context.Context, batch int) (int, error)
	PublishDeviceAuditAsync(ctx context.Context, userID uuid.UUID, event string, severity string, extras map[string]string)
	// GetActiveDeviceID trả về client_device_id của thiết bị đang hoạt động (chưa bị revoked) khớp với user và khóa công khai.
	GetActiveDeviceID(ctx context.Context, userID uuid.UUID, devicePublicKey string) (string, error)
}
