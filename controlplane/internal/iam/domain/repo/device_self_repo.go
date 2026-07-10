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

	// [COMMENT]: ResolveDeviceIDByFingerprint lấy client_device_id của thiết bị dựa trên fingerprint của public key
	ResolveDeviceIDByFingerprint(ctx context.Context, userID uuid.UUID, fingerprint string) (string, error)

	// [COMMENT]: RevokeMyDevice thu hồi một thiết bị cụ thể của user theo client_device_id
	RevokeMyDevice(ctx context.Context, clientDeviceID uuid.UUID, userID uuid.UUID, currentDeviceID uuid.UUID) error

	// [COMMENT]: RevokeMyOtherDevices thu hồi tất cả các thiết bị khác ngoại trừ thiết bị đang chỉ định theo client_device_id, trả về danh sách client_device_id đã thu hồi
	RevokeMyOtherDevices(ctx context.Context, userID uuid.UUID, keepDeviceID *uuid.UUID) ([]uuid.UUID, error)

	// [COMMENT]: BulkTouchDevices cập nhật hàng loạt trạng thái hoạt động (last_seen_at/ip/ua) cho nhiều thiết bị cùng lúc
	BulkTouchDevices(ctx context.Context, updates []iamEntity.DevicePresenceUpdate) error

	// [COMMENT]: EvictDevices thu hồi hàng loạt thiết bị của một user theo danh sách client_device_id và xóa refresh token tương ứng
	EvictDevices(ctx context.Context, userID uuid.UUID, clientDeviceIDs []string) error
}
