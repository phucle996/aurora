package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: DevicePlatformRepository định nghĩa các thao tác dữ liệu giám sát và quản lý thiết bị toàn hệ thống
type DevicePlatformRepository interface {
	// [COMMENT]: ListDevicesByUserIDWithHierarchy lấy danh sách thiết bị của một user phục vụ platform monitoring kèm hierarchy check trong 1 RTT CTE dưới dạng DevicePresence gọn nhẹ
	ListDevicesByUserIDWithHierarchy(ctx context.Context, userID uuid.UUID, callerLevel int32, limit int, offset int) ([]iamEntity.DevicePresence, error)
}
