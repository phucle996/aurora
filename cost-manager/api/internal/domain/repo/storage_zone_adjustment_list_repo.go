package billingRepoInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"
)

type StorageZoneAdjustmentListRepository interface {
	ListStorageZonePriceAdjustments(context.Context, entity.StorageZoneAdjustmentListQuery) ([]entity.StorageZoneAdjustmentListItem, bool, error)
}
