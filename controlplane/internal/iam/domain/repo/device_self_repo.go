package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: DeviceSelfRepository định nghĩa các thao tác dữ liệu liên quan đến thiết bị của chính user cá nhân
type DeviceSelfRepository interface {
	// [COMMENT]: UpsertLoginDevice lưu thông tin hoặc cập nhật thiết bị đăng nhập mới
	UpsertLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error)

	// [COMMENT]: ListDevicesByUserID lấy danh sách thiết bị của một user cá nhân dưới dạng DevicePresence gọn nhẹ
	ListDevicesByUserID(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]iamEntity.DevicePresence, error)

	// [COMMENT]: GetActiveDeviceID lấy client_device_id của thiết bị đang hoạt động dựa trên fingerprint của public key
	GetActiveDeviceID(ctx context.Context, userID uuid.UUID, fingerprint string) (string, error)

	// [COMMENT]: RevokeMyDevice thu hồi một thiết bị cụ thể của user theo client_device_id
	RevokeMyDevice(ctx context.Context, clientDeviceID uuid.UUID, userID uuid.UUID, currentDeviceID uuid.UUID) error

	// [COMMENT]: RevokeMyOtherDevices thu hồi tất cả các thiết bị khác ngoại trừ thiết bị đang chỉ định theo client_device_id, trả về danh sách client_device_id đã thu hồi
	RevokeMyOtherDevices(ctx context.Context, userID uuid.UUID, keepDeviceID *uuid.UUID) ([]uuid.UUID, error)

	// [COMMENT]: TouchDeviceLastSeen cập nhật mốc thời gian hoạt động cuối cùng của thiết bị
	TouchDeviceLastSeen(ctx context.Context, deviceID uuid.UUID) error

	// [COMMENT]: InsertAuditEvent ghi nhận sự kiện nhật ký bảo mật của user
	InsertAuditEvent(ctx context.Context, actorUserID *uuid.UUID, event string, severity string) error

	// [COMMENT]: EvictExcessDevices loại bỏ các thiết bị vượt quá số lượng tối đa cho phép
	EvictExcessDevices(ctx context.Context, userID uuid.UUID, cap int) ([]EvictedDevice, error)

	// [COMMENT]: ListUsersExceedingDeviceCap lấy danh sách ID người dùng có số lượng thiết bị vượt giới hạn
	ListUsersExceedingDeviceCap(ctx context.Context, cap int, limit int) ([]uuid.UUID, error)
}

// [COMMENT]: EvictedDevice là output của EvictExcessDevices
type EvictedDevice struct {
	DeviceID       uuid.UUID
	ClientDeviceID *string
}
