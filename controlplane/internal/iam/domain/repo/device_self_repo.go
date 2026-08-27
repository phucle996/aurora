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

}
