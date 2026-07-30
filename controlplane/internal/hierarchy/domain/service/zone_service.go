package hierarchySvcInterface

import (
	"context"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
)

type ZoneService interface {
	ListZones(context.Context, *hierarchyEntity.ListZones) ([]hierarchyEntity.ListZones, error)
	ListZoneCatalog(context.Context, *hierarchyEntity.ListZoneCatalog) ([]hierarchyEntity.ListZoneCatalog, error)
	ResolveZoneByCode(context.Context, *hierarchyEntity.ResolveZoneByCode) (*hierarchyEntity.ResolveZoneByCode, error)
	CreateZone(context.Context, *hierarchyEntity.CreateZone) (*hierarchyEntity.CreateZone, error)
	GetZoneDetail(context.Context, *hierarchyEntity.GetZoneDetail) ([]hierarchyEntity.GetZoneDetail, error)
	UpdateZoneStatus(context.Context, *hierarchyEntity.UpdateZoneStatus) (*hierarchyEntity.UpdateZoneStatus, error)
	DeleteZone(context.Context, *hierarchyEntity.DeleteZone) (*hierarchyEntity.DeleteZone, error)
	UpdateZoneService(context.Context, *hierarchyEntity.UpdateZoneService) (*hierarchyEntity.UpdateZoneService, error)
}
