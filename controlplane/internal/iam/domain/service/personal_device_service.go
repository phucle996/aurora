package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// PersonalDeviceService owns platform-authorized device audit in the
// `/personal` branch.
type PersonalDeviceService interface {
	// [COMMENT]: ListUserDevicesPlatform lấy danh sách thiết bị của một user phục vụ platform audit kèm hierarchy check
	ListUserDevicesPlatform(ctx context.Context, targetUserID uuid.UUID, callerLevel int32, limit int, offset int) (*iamEntity.PersonalDeviceListResult, error)
}
