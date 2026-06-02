package coreRepoInterface

import (
	"context"
	coreEntity "controlplane/internal/core/domain/entity"

	"github.com/google/uuid"
)

type ZoneRepository interface {
	ListZones(ctx context.Context) ([]coreEntity.Zone, error)
	GetZoneCatalog(ctx context.Context) ([]coreEntity.ZoneCatalog, error)
	CreateZone(ctx context.Context, zone coreEntity.Zone, svcs map[coreEntity.ZoneServiceType]bool, outboxEventID string, outboxPayload []byte, outboxVersion uint64) error
	GetZoneByID(ctx context.Context, id uuid.UUID) (*coreEntity.Zone, error)
	UpdateZoneStatus(ctx context.Context, id uuid.UUID, status coreEntity.ZoneStatus, outboxEventID string, outboxPayload []byte, outboxVersion uint64) error
	DeleteZone(ctx context.Context, id uuid.UUID, outboxEventID string, outboxPayload []byte, outboxVersion uint64) error
	HasDataplaneNodesByZone(ctx context.Context, zoneID uuid.UUID) (bool, error)
	HasEnabledZoneServicesByZone(ctx context.Context, zoneID uuid.UUID) (bool, error)
	ListZoneServicesByZoneID(ctx context.Context, zoneID uuid.UUID) ([]coreEntity.ZoneService, error)
	UpsertZoneServiceByZoneAndType(ctx context.Context, zoneID uuid.UUID, serviceType coreEntity.ZoneServiceType, enabled bool, outboxEventID string, outboxPayload []byte, outboxVersion uint64) (*coreEntity.ZoneService, error)
}
