package coreSvcInterface

import (
	"context"
	coreEntity "controlplane/internal/core/domain/entity"

	"github.com/google/uuid"
)

type ZoneService interface {
	ListZones(ctx context.Context) ([]coreEntity.Zone, error)
	GetZoneCatalog(ctx context.Context) ([]coreEntity.ZoneCatalog, error)

	// get zone detail for admin ui
	GetZoneDetailByID(ctx context.Context, id uuid.UUID) (*coreEntity.ZoneDetail, error)

	CreateZone(ctx context.Context, input coreEntity.CreateZoneInput) error
	UpdateZoneStatus(ctx context.Context, zoneID uuid.UUID, status coreEntity.ZoneStatus) (*coreEntity.Zone, error)
	DeleteZone(ctx context.Context, zoneID uuid.UUID) error
	ListZoneServices(ctx context.Context, zoneID uuid.UUID) ([]coreEntity.ZoneService, error)
	UpsertZoneService(ctx context.Context, zoneID uuid.UUID, serviceType coreEntity.ZoneServiceType, enabled bool) (*coreEntity.ZoneService, error)
}
