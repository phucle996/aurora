package coreRepoInterface

import (
	"context"
	coreEntity "controlplane/internal/hierarchy/domain/entity"

	"github.com/google/uuid"
)

type ZoneRepository interface {
	// trả danh sách zone cho trang quản trị web
	ListZones(ctx context.Context) ([]coreEntity.Zone, error)

	// RPCListZones truy vấn tối giản danh sách Zone (4 trường) để tối ưu hóa đồng bộ qua gRPC.
	// service này phục vụ acr lấy danh sách để chạy l1 cho phân giải zone context / trả zone catalog cho client
	RPCListZones(ctx context.Context) ([]coreEntity.RPCZone, error)

	// tạo zone mới
	CreateZone(ctx context.Context, zone coreEntity.Zone, svcs map[coreEntity.ZoneServiceType]bool) error

	// lấy zone chi tiết kèm theo các dịch vụ cho trang quản trị web
	GetZoneDetailByID(ctx context.Context, id uuid.UUID) (*coreEntity.ZoneDetail, error)

	// update trạng thái zone theo state machine
	UpdateZoneStatus(ctx context.Context, id uuid.UUID, status coreEntity.ZoneStatus, allowedOld []coreEntity.ZoneStatus) (string, error)

	// xóa zone
	DeleteZone(ctx context.Context, id uuid.UUID) (string, error)

	// bật tắt các dịch vụ trong zone
	UpdateZoneService(ctx context.Context, zoneID uuid.UUID, serviceType coreEntity.ZoneServiceType, enabled bool) (*coreEntity.ZoneService, string, error)
}
