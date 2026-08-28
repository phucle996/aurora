package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: SelfDeviceRepository quản lý lưu trữ và truy vấn thiết bị bền vững cho chủ sở hữu danh tính (/me scope)
type SelfDeviceRepository interface {
	// [COMMENT]: UpsertLoginDevice lưu thông tin hoặc cập nhật thiết bị đăng nhập mới
	UpsertLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error)

	// [COMMENT]: ListDevicesByUserID lấy danh sách thiết bị của một user cá nhân dưới dạng DevicePresence gọn nhẹ
	ListDevicesByUserID(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]iamEntity.DevicePresence, error)

	// [COMMENT]: RevokeSelfDevice thu hồi 1 thiết bị cụ thể và xóa refresh token liên quan
	RevokeSelfDevice(ctx context.Context, command iamEntity.DeviceRuntimeRevokeDevice) (iamEntity.DeviceRuntimeRevokeResult, error)

	// [COMMENT]: RevokeOtherSelfDevices thu hồi tất cả thiết bị khác và xóa refresh tokens liên quan
	RevokeOtherSelfDevices(ctx context.Context, command iamEntity.DeviceRuntimeRevokeOthers) (iamEntity.DeviceRuntimeRevokeOthersResult, error)

	ApplyDevicePresenceProjection(ctx context.Context, updates []iamEntity.DevicePresenceUpdate) error
	ApplyDeviceSessionCapacityEviction(ctx context.Context, userID uuid.UUID, clientDeviceIDs []uuid.UUID) error
}
