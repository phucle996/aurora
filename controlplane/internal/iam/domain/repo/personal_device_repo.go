package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// PersonalDeviceRepository owns the platform-authorized personal branch for
// auditing another user's devices.
type PersonalDeviceRepository interface {
	// [COMMENT]: ListDevicesByUserID lấy projection riêng của platform audit kèm hierarchy check trong 1 RTT CTE.
	ListDevicesByUserID(ctx context.Context, userID uuid.UUID, callerLevel int32, limit int, offset int) ([]iamEntity.PersonalDeviceListItem, error)
}
