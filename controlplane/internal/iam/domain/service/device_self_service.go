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

	// [COMMENT]: RevokeMyDevice thu hồi quyền truy cập của một thiết bị cụ thể thuộc sở hữu user cá nhân theo client_device_id
	RevokeMyDevice(ctx context.Context, userID uuid.UUID, clientDeviceID uuid.UUID, currentDeviceID uuid.UUID) error

	// [COMMENT]: LogoutOtherDevices đăng xuất khỏi toàn bộ thiết bị khác
	LogoutOtherDevices(ctx context.Context, userID uuid.UUID, currentDeviceID uuid.UUID) (int64, error)

	// [COMMENT]: RegisterLoginDevice đăng ký thiết bị đăng nhập
	RegisterLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error)

	// [COMMENT]: BulkTouchDevices cập nhật hàng loạt trạng thái hoạt động cho nhiều thiết bị — được gọi từ Shared Redis Consumer
	BulkTouchDevices(ctx context.Context, updates []iamEntity.DevicePresenceUpdate) error

	// [COMMENT]: ResolveDeviceIDByKey trả về client_device_id của thiết bị khớp với user và khóa công khai
	ResolveDeviceIDByKey(ctx context.Context, userID uuid.UUID, devicePublicKey string) (string, error)
	// [COMMENT]: EvictDevicesByClientDeviceIDs thu hồi hàng loạt thiết bị của một user dựa trên danh sách client_device_id và xóa token
	EvictDevices(ctx context.Context, userID uuid.UUID, clientDeviceIDs []string) error
}
