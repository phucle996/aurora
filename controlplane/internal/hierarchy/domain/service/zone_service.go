package zoneSvcInterface

import (
	"context"
	coreEntity "controlplane/internal/hierarchy/domain/entity"

	"github.com/google/uuid"
)

type ZoneService interface {
	// ListZones trả về danh sách tất cả các zone
	ListZones(ctx context.Context) ([]coreEntity.Zone, error)
	// AcrListZones phục vụ luồng sync sang ACR chỉ lấy 4 thuộc tính (ID, Code, Name, Status)
	AcrListZones(ctx context.Context) ([]coreEntity.RPCZone, error)

	// get zone detail for admin ui
	GetZoneDetailByID(ctx context.Context, id uuid.UUID) (*coreEntity.ZoneDetail, error)

	CreateZone(ctx context.Context, input coreEntity.CreateZoneInput) error
	UpdateZoneStatus(ctx context.Context, zoneID uuid.UUID, status coreEntity.ZoneStatus) error
	DeleteZone(ctx context.Context, zoneID uuid.UUID) error

	// bật tắt các dịch vụ trong zone
	UpdateZoneService(ctx context.Context, zoneID uuid.UUID, serviceType coreEntity.ZoneServiceType, enabled bool) (*coreEntity.ZoneService, error)
}
