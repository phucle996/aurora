// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/service/zone_service.go
//            Đặc Tả Nghiệp Vụ Quản Lý Vòng Đời Zone & Zone Services
// ======================================================================================================
//
// 📜 THIẾT KẾ & TÁCH BIỆT TRÁCH NHIỆM:
//   - ZoneService chỉ tập trung vào logic nghiệp vụ và tương tác với Database (Source of Truth).
//   - Toàn bộ logic quản lý bộ nhớ đệm L2 RAM Cache, đồng bộ phiên bản (Versioning) và cơ chế phát tán
//     (Redis Pub/Sub Fanout) được che giấu hoàn toàn phía sau lớp cache (ZoneFanoutCache).
//
// ======================================================================================================

package coreSvcImpl

import (
	"context"
	"time"

	"controlplane/internal/cacheengine"
	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreSvcInterface "controlplane/internal/core/domain/service"
	coreMetric "controlplane/internal/core/metrics"
	coreTaxonomy "controlplane/internal/core/taxonomy"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
)

type ZoneService struct {
	repo       coreRepoInterface.ZoneRepository
	l1Registry *cacheengine.CacheRegistry // Tích hợp CacheRegistry từ cache-engine chứa toàn bộ L1, L2, Fanout, Exec
}

func NewZoneService(
	repo coreRepoInterface.ZoneRepository,
	l1Registry *cacheengine.CacheRegistry,
) coreSvcInterface.ZoneService {
	return &ZoneService{
		repo:       repo,
		l1Registry: l1Registry,
	}
}

func (s *ZoneService) ListZones(ctx context.Context) ([]coreEntity.Zone, error) {
	return s.repo.ListZones(ctx)
}

// RPCListZones phục vụ luồng gRPC sync sang ACL chỉ lấy 4 thuộc tính (ID, Code, Name, Status).
// Triển khai này giúp tối ưu hóa hiệu năng, giảm dung lượng payload khi đồng bộ danh sách Zone qua biên.
func (s *ZoneService) RPCListZones(ctx context.Context) ([]coreEntity.RPCZone, error) {
	// [COMMENT]: Gọi xuống repository RPCListZones chuyên biệt để tránh tải các trường không cần thiết
	zones, err := s.repo.RPCListZones(ctx)
	if err != nil {
		// [COMMENT]: Ghi nhận lỗi truy vấn cơ sở dữ liệu
		coreMetric.ObserveZoneOperation("rpc_list_zones", coreTaxonomy.OutcomeRepoFailed)
		return nil, err
	}
	// [COMMENT]: Ghi nhận truy vấn cơ sở dữ liệu thành công
	coreMetric.ObserveZoneOperation("rpc_list_zones", coreTaxonomy.OutcomeRepoSuccess)
	return zones, nil
}

// CreateZone tạo zone mới + cập nhật cache (tự động đồng bộ các replica).
func (s *ZoneService) CreateZone(ctx context.Context, input coreEntity.CreateZoneInput) error {
	now := time.Now().UTC()
	zoneID, err := uuid.NewV7()
	if err != nil {
		return apperr.Wrap(coreTaxonomy.ErrZoneInvalidInput, err)
	}
	zone := coreEntity.Zone{
		ID:          zoneID,
		Code:        input.Code,
		Name:        input.Name,
		Location:    input.Location,
		Description: input.Description,
		Status:      coreEntity.ZoneStatusPlanned,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	svcs := map[coreEntity.ZoneServiceType]bool{
		coreEntity.ZoneServiceTypeHypervisor: input.EnableHypervisor,
		coreEntity.ZoneServiceTypeStorage:    input.EnableStorage,
		coreEntity.ZoneServiceTypeMail:       input.EnableMail,
		coreEntity.ZoneServiceTypeKubernetes: input.EnableKubernetes,
		coreEntity.ZoneServiceTypeAI:         input.EnableAI,
		coreEntity.ZoneServiceTypeDatabase:   input.EnableDatabase,
	}

	if err := s.repo.CreateZone(ctx, zone, svcs); err != nil {
		return err
	}

	// [COMMENT]: Chỉ dọn dẹp cache zone_catalog phục vụ API public, không còn dùng cache cho zone_list nữa
	s.l1Registry.L1.Delete("zone_catalog")
	coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateSuccess)

	// [COMMENT]: Phát tán thông báo xóa cache zone_catalog tới các replica khác qua pub/sub
	detachedCtx := context.WithoutCancel(ctx)
	go func() {
		if s.l1Registry != nil && s.l1Registry.Fanout != nil {
			if _, err := s.l1Registry.Fanout.Publish(detachedCtx, "zone_catalog", nil); err != nil {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
			} else {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
			}
		}
	}()

	return nil
}

// GetZoneDetailByID lấy thông tin chi tiết của một Zone kèm theo tất cả các dịch vụ của nó.
func (s *ZoneService) GetZoneDetailByID(ctx context.Context, id uuid.UUID) (*coreEntity.ZoneDetail, error) {
	detail, err := s.repo.GetZoneDetailByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return detail, nil
}

// UpdateZoneStatus chuyển trạng thái zone.
func (s *ZoneService) UpdateZoneStatus(ctx context.Context, zoneID uuid.UUID, toStatus coreEntity.ZoneStatus) error {
	// allowed quy định bản đồ chuyển đổi trạng thái hợp lệ (State Machine Transitions).
	// Key: Trạng thái đích (toStatus) - Value: Danh sách các trạng thái cũ được phép chuyển đổi sang trạng thái đích.
	allowed := map[coreEntity.ZoneStatus][]coreEntity.ZoneStatus{
		// Có thể đưa zone quay lại trạng thái Planned từ Active hoặc Disabled.
		coreEntity.ZoneStatusPlanned: {coreEntity.ZoneStatusActive, coreEntity.ZoneStatusDisabled},
		// [BUG FIX]: Bổ sung ZoneStatusPlanned vào danh sách trạng thái cũ hợp lệ để cho phép kích hoạt (Active) một zone mới được tạo.
		coreEntity.ZoneStatusActive: {coreEntity.ZoneStatusPlanned, coreEntity.ZoneStatusDraining, coreEntity.ZoneStatusMaintenance, coreEntity.ZoneStatusDisabled},
		// Trạng thái Draining (xả tải) có thể kích hoạt từ Active, Maintenance hoặc Disabled.
		coreEntity.ZoneStatusDraining: {coreEntity.ZoneStatusActive, coreEntity.ZoneStatusMaintenance, coreEntity.ZoneStatusDisabled},
		// Trạng thái Bảo trì (Maintenance) chỉ cho phép từ Active hoặc Disabled.
		coreEntity.ZoneStatusMaintenance: {coreEntity.ZoneStatusActive, coreEntity.ZoneStatusDisabled},
		// zone chỉ có thể disabled từ active
		coreEntity.ZoneStatusDisabled: {coreEntity.ZoneStatusActive},
	}

	allowedOld := append(allowed[toStatus], toStatus)
	err := s.repo.UpdateZoneStatus(ctx, zoneID, toStatus, allowedOld)
	if err != nil {
		return err
	}

	// [COMMENT]: Chỉ dọn dẹp cache zone_catalog cho API public
	s.l1Registry.L1.Delete("zone_catalog")
	coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateSuccess)

	// [COMMENT]: Phát tán thông báo xóa cache zone_catalog tới các replica khác
	detachedCtx := context.WithoutCancel(ctx)
	go func() {
		if s.l1Registry != nil && s.l1Registry.Fanout != nil {
			if _, err := s.l1Registry.Fanout.Publish(detachedCtx, "zone_catalog", nil); err != nil {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
			} else {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
			}
		}
	}()

	return nil
}

// DeleteZone xóa zone khi đủ 3 preconditions.
func (s *ZoneService) DeleteZone(ctx context.Context, zoneID uuid.UUID) error {
	_, err := s.repo.DeleteZone(ctx, zoneID)
	if err != nil {
		return err
	}

	// [COMMENT]: Chỉ dọn dẹp cache zone_catalog phục vụ API public
	s.l1Registry.L1.Delete("zone_catalog")
	coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateSuccess)

	// [COMMENT]: Phát tán thông báo xóa cache zone_catalog tới các replica khác
	detachedCtx := context.WithoutCancel(ctx)
	go func() {
		if s.l1Registry != nil && s.l1Registry.Fanout != nil {
			if _, err := s.l1Registry.Fanout.Publish(detachedCtx, "zone_catalog", nil); err != nil {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
			} else {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
			}
		}
	}()

	return nil
}

// ListZoneServices trả danh sách tất cả zone services của một zone.
func (s *ZoneService) ListZoneServices(ctx context.Context, zoneID uuid.UUID) ([]coreEntity.ZoneService, error) {

	svcs, err := s.repo.ListZoneServicesByZoneID(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	return svcs, nil
}

// UpsertZoneService cập nhật enabled/disabled của một service trong zone.
func (s *ZoneService) UpsertZoneService(ctx context.Context, zoneID uuid.UUID, serviceType coreEntity.ZoneServiceType, enabled bool) (*coreEntity.ZoneService, error) {
	svc, _, err := s.repo.UpsertZoneServiceByZoneAndType(ctx, zoneID, serviceType, enabled)
	if err != nil {
		return nil, err
	}

	// [COMMENT]: Chỉ dọn dẹp cache zone_catalog phục vụ API public
	if ok := s.l1Registry.L1.Delete("zone_catalog"); ok {
		coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateSuccess)
	} else {
		coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateFailed)
	}

	detachedCtx := context.WithoutCancel(ctx)
	go func() {
		if s.l1Registry != nil && s.l1Registry.Fanout != nil {
			if _, err := s.l1Registry.Fanout.Publish(detachedCtx, "zone_catalog", nil); err != nil {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
			} else {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
			}
		}
	}()

	return svc, nil
}
