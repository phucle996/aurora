package service

import (
	"context"
	entity "controlplane/internal/hierarchy/domain/entity"

	"github.com/google/uuid"
)

type ZoneService interface {
	// ListZones trả về danh sách tất cả các zone
	ListZones(ctx context.Context) ([]entity.Zone, error)
	// AcrListZones phục vụ luồng sync sang ACR chỉ lấy 4 thuộc tính (ID, Code, Name, Status)
	AcrListZones(ctx context.Context) ([]entity.RPCZone, error)
	// AcrResolveZone phân giải một Zone cụ thể theo mã code phục vụ ACR
	AcrResolveZone(ctx context.Context, code string) (*entity.RPCZone, error)

	// get zone detail for admin ui
	GetZoneDetailByID(ctx context.Context, id uuid.UUID) (*entity.ZoneDetail, error)

	CreateZone(ctx context.Context, input entity.CreateZoneInput) error
	UpdateZoneStatus(ctx context.Context, zoneID uuid.UUID, status entity.ZoneStatus) error
	DeleteZone(ctx context.Context, zoneID uuid.UUID) error

	// bật tắt các dịch vụ trong zone
	UpdateZoneService(ctx context.Context, zoneID uuid.UUID, serviceType entity.ZoneServiceType, enabled bool) (*entity.ZoneService, error)
}
