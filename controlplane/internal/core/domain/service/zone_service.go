package coreSvcInterface

import (
	"context"
	coreEntity "controlplane/internal/core/domain/entity"
)

type ZoneService interface {
	ListZones(ctx context.Context) ([]coreEntity.Zone, error)
	CreateZone(ctx context.Context, code, name string, status *coreEntity.ZoneStatus) (*coreEntity.Zone, error)
	UpdateZoneStatus(ctx context.Context, zoneID string, status coreEntity.ZoneStatus) (*coreEntity.Zone, error)
	DeleteZone(ctx context.Context, zoneID string) error
	ListZoneServices(ctx context.Context, zoneID string) ([]coreEntity.ZoneService, error)
	UpsertZoneService(ctx context.Context, zoneID string, serviceType string, enabled bool) (*coreEntity.ZoneService, error)
}
