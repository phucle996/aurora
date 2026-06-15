package coreRepoInterface

import (
	"context"
	coreEntity "controlplane/internal/core/domain/entity"

	"github.com/google/uuid"
)

type ZoneRepository interface {
	ListZones(ctx context.Context) ([]coreEntity.Zone, error)
	GetZoneCatalog(ctx context.Context) ([]coreEntity.ZoneCatalog, error)
	CreateZone(ctx context.Context, zone coreEntity.Zone, svcs map[coreEntity.ZoneServiceType]bool) error
	GetZoneByID(ctx context.Context, id uuid.UUID) (*coreEntity.Zone, error)
	GetZoneDetailByID(ctx context.Context, id uuid.UUID) (*coreEntity.ZoneDetail, error)
	GetZoneIDByCode(ctx context.Context, code string) (uuid.UUID, error)
	UpdateZoneStatus(ctx context.Context, id uuid.UUID, status coreEntity.ZoneStatus, allowedOld []coreEntity.ZoneStatus) error
	DeleteZone(ctx context.Context, id uuid.UUID) (string, error)
	HasEnabledZoneServicesByZone(ctx context.Context, zoneID uuid.UUID) (bool, error)
	ListZoneServicesByZoneID(ctx context.Context, zoneID uuid.UUID) ([]coreEntity.ZoneService, error)
	UpsertZoneServiceByZoneAndType(ctx context.Context, zoneID uuid.UUID, serviceType coreEntity.ZoneServiceType, enabled bool) (*coreEntity.ZoneService, string, error)
}
