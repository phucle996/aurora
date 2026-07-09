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

	// [COMMENT]: TouchDeviceLastSeen cập nhật mốc thời gian hoạt động cuối cùng của thiết bị
	TouchDeviceLastSeen(ctx context.Context, deviceID uuid.UUID) error

	// [COMMENT]: EvictExcessDevicesIfNeeded thu hồi bớt các thiết bị cũ nếu vượt ngưỡng giới hạn tối đa
	EvictExcessDevicesIfNeeded(ctx context.Context, userID uuid.UUID)

	// [COMMENT]: ReconcileDeviceCap định kỳ dọn dẹp các thiết bị vượt ngưỡng
	ReconcileDeviceCap(ctx context.Context, batch int) (int, error)

	// [COMMENT]: PublishDeviceAuditAsync ghi nhận sự kiện nhật ký thiết bị bất đồng bộ
	PublishDeviceAuditAsync(ctx context.Context, userID uuid.UUID, event string, severity string, extras map[string]string)

	// [COMMENT]: GetActiveDeviceID trả về client_device_id của thiết bị đang hoạt động khớp với user và khóa công khai
	GetActiveDeviceID(ctx context.Context, userID uuid.UUID, devicePublicKey string) (string, error)
}
