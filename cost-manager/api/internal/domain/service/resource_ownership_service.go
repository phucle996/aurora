package billingSvcInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"
)

// ResourceOwnershipService là interface điều phối nghiệp vụ đồng bộ quyền sở hữu tài nguyên đám mây vào Cost Manager.
type ResourceOwnershipService interface {
	ProcessResourceOwnershipEvent(ctx context.Context, event *entity.ResourceOwnershipEvent) error
}
