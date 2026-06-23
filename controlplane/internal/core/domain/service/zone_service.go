package coreSvcInterface

import (
	"context"
	coreEntity "controlplane/internal/core/domain/entity"

	"github.com/google/uuid"
)

type ZoneService interface {
	ListZones(ctx context.Context) ([]coreEntity.Zone, error)
	// RPCListZones phục vụ luồng gRPC sync sang ACL chỉ lấy 4 thuộc tính (ID, Code, Name, Status)
	RPCListZones(ctx context.Context) ([]coreEntity.RPCZone, error)

	// get zone detail for admin ui
	GetZoneDetailByID(ctx context.Context, id uuid.UUID) (*coreEntity.ZoneDetail, error)

	CreateZone(ctx context.Context, input coreEntity.CreateZoneInput) error
	UpdateZoneStatus(ctx context.Context, zoneID uuid.UUID, status coreEntity.ZoneStatus) error
	DeleteZone(ctx context.Context, zoneID uuid.UUID) error
	ListZoneServices(ctx context.Context, zoneID uuid.UUID) ([]coreEntity.ZoneService, error)
	UpsertZoneService(ctx context.Context, zoneID uuid.UUID, serviceType coreEntity.ZoneServiceType, enabled bool) (*coreEntity.ZoneService, error)
}
