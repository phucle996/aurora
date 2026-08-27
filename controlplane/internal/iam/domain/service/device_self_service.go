package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: DeviceSelfService quản lý nghiệp vụ liên quan đến thiết bị của chính user cá nhân
type DeviceSelfService interface {
	// [COMMENT]: ListMyDevices lấy danh sách thiết bị của user cá nhân
	ListMyDevices(ctx context.Context, userID uuid.UUID, limit int, offset int) (*iamEntity.DeviceListResult, error)

	// [COMMENT]: RegisterLoginDevice đăng ký thiết bị đăng nhập
	RegisterLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error)

	// [COMMENT]: ResolveDeviceIDByKey trả về client_device_id của thiết bị khớp với user và khóa công khai
	ResolveDeviceIDByKey(ctx context.Context, userID uuid.UUID, devicePublicKey string) (string, error)
}
