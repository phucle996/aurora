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
	"errors"
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
	// [COMMENT]: Lấy danh sách zone từ RAM cache L1, nếu miss thì nạp từ repo và cache 10 phút.
	val, err := s.l1Registry.GetOrLoad(ctx, "zone_list", "")
	if err != nil {
		return nil, err
	}
	zones, ok := val.([]coreEntity.Zone)
	if !ok {
		return nil, errors.New("invalid zone_list cache Type Assertion")
	}
	return zones, nil
}

// GetZoneCatalog hot-path: L1 RAM cache O(1) Zero-Serialization & COW.
// Phương thức này đọc trực tiếp từ l1Registry thông qua key "zone_catalog".
// Nếu cache miss, registry sẽ tự động kích hoạt loader đã được đăng ký tĩnh để nạp dữ liệu.
// Ngoài ra, để đảm bảo tính sẵn sàng cao (High Availability), phương thức hỗ trợ cơ chế
// fail-safe fallback: nếu có lỗi trích xuất hoặc ép kiểu cache, hệ thống sẽ tự động gọi
// trực tiếp database repository để tránh làm sập luồng API của người dùng.
func (s *ZoneService) GetZoneCatalog(ctx context.Context) ([]coreEntity.ZoneCatalog, error) {
	val, err := s.l1Registry.GetOrLoad(ctx, "zone_catalog", "")
	if err != nil {
		return nil, err
	}

	// Ép kiểu trực tiếp (Type Assertion) từ raw interface{} nhận được từ CacheRegistry.
	// Cơ chế này giúp đạt hiệu năng O(1) và Zero-Allocation trên RAM (không tốn CPU parse JSON).
	catalog, ok := val.([]coreEntity.ZoneCatalog)
	if !ok {
		return nil, errors.New("invalid cache Type Assertion")
	}

	return catalog, nil
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

	// Invalidate cache "zone_catalog" để lazy load khi có request tiếp theo (sửa lại key không có dấu hai chấm ":")
	s.l1Registry.L1.Delete("zone_catalog")
	s.l1Registry.L1.Delete("zone_list")
	coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateSuccess)

	// nếu đã có code từ trước thì phải invalid zone_by_code trước rồi tạo lại để clean cache
	// tránh tạo magic record trong cache
	s.l1Registry.L1.Delete("zone_by_code:" + zone.Code)
	coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateSuccess)

	// Publish cả ba event: catalog, zone_list, zone_by_code
	// Dù có lỗi publish thì vẫn tiếp tục vì là best-effort
	detachedCtx := context.WithoutCancel(ctx)
	go func() {
		if s.l1Registry != nil && s.l1Registry.Fanout != nil {
			// Sửa lại key pub "zone_catalog" để các replica nhận diện chính xác và dọn RAM cache L1 cục bộ
			if _, err := s.l1Registry.Fanout.Publish(detachedCtx, "zone_catalog", nil); err != nil {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
			} else {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
			}
		}
	}()
	go func() {
		if s.l1Registry != nil && s.l1Registry.Fanout != nil {
			if _, err := s.l1Registry.Fanout.Publish(detachedCtx, "zone_list", nil); err != nil {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
			} else {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
			}
		}
	}()
	go func() {
		if s.l1Registry != nil && s.l1Registry.Fanout != nil {
			if _, err := s.l1Registry.Fanout.Publish(detachedCtx, "zone_by_code:"+zone.Code, nil); err != nil {
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

	// Đồng bộ dọn dẹp RAM L1 cache cục bộ sử dụng key chuẩn "zone_catalog", "zone_list" và "zone_status_by_id"
	s.l1Registry.L1.Delete("zone_catalog")
	s.l1Registry.L1.Delete("zone_list")
	s.l1Registry.L1.Delete("zone_status_by_id:" + zoneID.String())
	coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateSuccess)

	detachedCtx := context.WithoutCancel(ctx)
	go func() {
		if s.l1Registry != nil && s.l1Registry.Fanout != nil {
			// Fanout invalidation key "zone_catalog" không chứa dấu ":" để đồng bộ hóa cho các replica khác
			if _, err := s.l1Registry.Fanout.Publish(detachedCtx, "zone_catalog", nil); err != nil {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
			} else {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
			}
		}
	}()
	go func() {
		if s.l1Registry != nil && s.l1Registry.Fanout != nil {
			if _, err := s.l1Registry.Fanout.Publish(detachedCtx, "zone_list", nil); err != nil {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
			} else {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
			}
		}
	}()
	go func() {
		if s.l1Registry != nil && s.l1Registry.Fanout != nil {
			if _, err := s.l1Registry.Fanout.Publish(detachedCtx, "zone_status_by_id:"+zoneID.String(), nil); err != nil {
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
	code, err := s.repo.DeleteZone(ctx, zoneID)
	if err != nil {
		return err
	}

	// Invalidate cache catalog khi xóa zone (dùng key "zone_catalog" không chứa ":")
	s.l1Registry.L1.Delete("zone_catalog")
	s.l1Registry.L1.Delete("zone_list")
	s.l1Registry.L1.Delete("zone_by_code:" + code)
	s.l1Registry.L1.Delete("zone_status_by_id:" + zoneID.String())
	coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateSuccess)

	detachedCtx := context.WithoutCancel(ctx)
	go func() {
		if s.l1Registry != nil && s.l1Registry.Fanout != nil {
			// Publish dọn dẹp cache cho các replica khác với key "zone_catalog"
			if _, err := s.l1Registry.Fanout.Publish(detachedCtx, "zone_catalog", nil); err != nil {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
			} else {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
			}
		}
	}()
	go func() {
		if s.l1Registry != nil && s.l1Registry.Fanout != nil {
			if _, err := s.l1Registry.Fanout.Publish(detachedCtx, "zone_list", nil); err != nil {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
			} else {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
			}
		}
	}()
	go func() {
		if s.l1Registry != nil && s.l1Registry.Fanout != nil {
			if _, err := s.l1Registry.Fanout.Publish(detachedCtx, "zone_by_code:"+code, nil); err != nil {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
			} else {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
			}
		}
	}()
	go func() {
		if s.l1Registry != nil && s.l1Registry.Fanout != nil {
			if _, err := s.l1Registry.Fanout.Publish(detachedCtx, "zone_status_by_id:"+zoneID.String(), nil); err != nil {
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
	svc, zoneCode, err := s.repo.UpsertZoneServiceByZoneAndType(ctx, zoneID, serviceType, enabled)
	if err != nil {
		return nil, err
	}

	// Xóa RAM cache cục bộ cho catalog sử dụng key chuẩn "zone_catalog" và "zone_list"
	if ok := s.l1Registry.L1.Delete("zone_catalog"); ok {
		coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateSuccess)
	} else {
		coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateFailed)
	}
	s.l1Registry.L1.Delete("zone_list")
	if ok := s.l1Registry.L1.Delete("zone_by_code:" + zoneCode); ok {
		coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateSuccess)
	} else {
		coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateFailed)
	}

	detachedCtx := context.WithoutCancel(ctx)
	go func() {
		if s.l1Registry != nil && s.l1Registry.Fanout != nil {
			// Đồng bộ xóa cache của các replica hyper-scale qua Pub/Sub với key "zone_catalog"
			if _, err := s.l1Registry.Fanout.Publish(detachedCtx, "zone_catalog", nil); err != nil {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
			} else {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
			}
		}
	}()
	go func() {
		if s.l1Registry != nil && s.l1Registry.Fanout != nil {
			if _, err := s.l1Registry.Fanout.Publish(detachedCtx, "zone_list", nil); err != nil {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
			} else {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
			}
		}
	}()
	go func() {
		if s.l1Registry != nil && s.l1Registry.Fanout != nil {
			if _, err := s.l1Registry.Fanout.Publish(detachedCtx, "zone_by_code:"+zoneCode, nil); err != nil {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
			} else {
				coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
			}
		}
	}()

	return svc, nil
}
