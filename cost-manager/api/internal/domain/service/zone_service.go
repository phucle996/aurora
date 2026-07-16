package service

import (
	"context"

	"cost-manager/api/internal/domain/entity"
)

type ZoneService interface {
	ListZones(ctx context.Context) ([]entity.Zone, error)
}
