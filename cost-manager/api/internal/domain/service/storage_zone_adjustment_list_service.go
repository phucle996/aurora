package billingSvcInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"
)

type StorageZoneAdjustmentListService interface {
	ListStorageZonePriceAdjustments(context.Context, entity.StorageZoneAdjustmentListQuery) (*entity.StorageZoneAdjustmentListResult, error)
}
