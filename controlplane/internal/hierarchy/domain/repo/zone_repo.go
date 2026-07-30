package repo

import (
	"context"
	entity "controlplane/internal/hierarchy/domain/entity"

	"github.com/google/uuid"
)

type ZoneRepository interface {
	// trả danh sách zone cho trang quản trị web
	ListZones(ctx context.Context) ([]entity.Zone, error)

	// AcrListZones truy vấn tối giản danh sách Zone (4 trường) để tối ưu hóa đồng bộ qua NATS.
	// service này phục vụ acr lấy danh sách để chạy l1 cho phân giải zone context / trả zone catalog cho client
	AcrListZones(ctx context.Context) ([]entity.RPCZone, error)

	// tạo zone mới
	CreateZone(ctx context.Context, zone entity.Zone, svcs map[entity.ZoneServiceType]bool) error

	// lấy zone chi tiết kèm theo các dịch vụ cho trang quản trị web
	GetZoneDetailByID(ctx context.Context, id uuid.UUID) (*entity.ZoneDetail, error)

	// update trạng thái zone theo state machine
	UpdateZoneStatus(ctx context.Context, id uuid.UUID, status entity.ZoneStatus, allowedOld []entity.ZoneStatus) (string, error)

	// xóa zone
	DeleteZone(ctx context.Context, id uuid.UUID) (string, error)

	// bật tắt các dịch vụ trong zone
	UpdateZoneService(ctx context.Context, zoneID uuid.UUID, serviceType entity.ZoneServiceType, enabled bool) (*entity.ZoneService, string, error)
}
