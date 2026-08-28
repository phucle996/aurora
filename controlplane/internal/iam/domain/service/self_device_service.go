package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: SelfDeviceService quản lý các thao tác thiết bị ở phạm vi chủ sở hữu danh tính (/me scope)
type SelfDeviceService interface {
	// [COMMENT]: ListMyDevices lấy danh sách thiết bị của user cá nhân
	ListMyDevices(ctx context.Context, userID uuid.UUID, limit int, offset int) (*iamEntity.DeviceListResult, error)

	// [COMMENT]: RegisterLoginDevice đăng ký thiết bị đăng nhập
	RegisterLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error)

	// [COMMENT]: ResolveDeviceIDByKey trả về client_device_id của thiết bị khớp với user và khóa công khai
	ResolveDeviceIDByKey(ctx context.Context, userID uuid.UUID, devicePublicKey string) (*uuid.UUID, error)

	// [COMMENT]: RevokeMyDevice thu hồi 1 thiết bị cụ thể và xóa runtime session trực tiếp trên Auth Redis
	RevokeMyDevice(ctx context.Context, userID uuid.UUID, clientDeviceID uuid.UUID, currentDeviceID uuid.UUID) error

	// [COMMENT]: LogoutOtherDevices thu hồi tất cả thiết bị khác và xóa runtime sessions trực tiếp trên Auth Redis
	LogoutOtherDevices(ctx context.Context, userID uuid.UUID, currentDeviceID uuid.UUID) (int64, error)

	ApplyDevicePresenceProjection(ctx context.Context, updates []iamEntity.DevicePresenceUpdate) error
	ApplyDeviceSessionCapacityEviction(ctx context.Context, userID uuid.UUID, clientDeviceIDs []uuid.UUID) error
}
