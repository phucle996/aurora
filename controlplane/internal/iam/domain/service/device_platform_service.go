package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: DevicePlatformService quản lý nghiệp vụ audit và giám sát thiết bị toàn platform
type DevicePlatformService interface {
	// [COMMENT]: ListUserDevicesPlatform lấy danh sách thiết bị của một user phục vụ platform audit kèm hierarchy check
	ListUserDevicesPlatform(ctx context.Context, targetUserID uuid.UUID, callerLevel int32, limit int, offset int) (*iamEntity.DeviceListResult, error)
}
