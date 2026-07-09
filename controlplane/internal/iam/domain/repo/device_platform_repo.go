package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: DevicePlatformRepository định nghĩa các thao tác dữ liệu giám sát và quản lý thiết bị toàn hệ thống
type DevicePlatformRepository interface {
	// [COMMENT]: ListDevicesByUserIDWithHierarchy lấy danh sách thiết bị của một user phục vụ platform audit kèm hierarchy check trong 1 RTT CTE dưới dạng DevicePresence gọn nhẹ
	ListDevicesByUserIDWithHierarchy(ctx context.Context, userID uuid.UUID, callerLevel int32, limit int, offset int) ([]iamEntity.DevicePresence, error)

	// [COMMENT]: InsertAuditEvent ghi nhận nhật ký hoạt động hệ thống liên quan đến thiết bị
	InsertAuditEvent(ctx context.Context, actorUserID *uuid.UUID, event string, severity string) error
}
