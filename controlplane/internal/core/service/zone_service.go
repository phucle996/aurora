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
	l1Registry *cacheengine.CacheRegistry // Tích hợp CacheRegistry từ cache-engine
	l1Fanout   *cacheengine.RedisFanout   // Tích hợp RedisFanout độc lập
}

func NewZoneService(
	repo coreRepoInterface.ZoneRepository,
	l1Registry *cacheengine.CacheRegistry,
	l1Fanout *cacheengine.RedisFanout,
) coreSvcInterface.ZoneService {
	return &ZoneService{
		repo:       repo,
		l1Registry: l1Registry,
		l1Fanout:   l1Fanout,
	}
}

func (s *ZoneService) ListZones(ctx context.Context) ([]coreEntity.Zone, error) {
	return s.repo.ListZones(ctx)
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

	// invalidate cache để lazy load khi có request tiếp theo
	s.l1Registry.InvalidateLocal(ctx, "zone_catalog:")
	coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateSuccess)

	// nếu đã có code từ trước thì phải invalid zone_by_code trước rồi tạo lại để clean cache
	// tránh tạo magic record trong cache
	s.l1Registry.InvalidateLocal(ctx, "zone_by_code:"+zone.Code)
	coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateSuccess)

	// Publish cả hai event: một cho catalog, một cho zone_by_code
	// Dù có lỗi publish thì vẫn tiếp tục vì là best-effort
	detachedCtx := context.WithoutCancel(ctx)
	go func() {
		if _, err := s.l1Fanout.Publish(detachedCtx, "zone_catalog:", nil); err != nil {
			coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
		} else {
			coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
		}
	}()
	go func() {
		if _, err := s.l1Fanout.Publish(detachedCtx, "zone_by_code:"+zone.Code, nil); err != nil {
			coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
		} else {
			coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
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
func (s *ZoneService) UpdateZoneStatus(ctx context.Context, zoneID uuid.UUID, toStatus coreEntity.ZoneStatus) (*coreEntity.Zone, error) {
	allowed := map[coreEntity.ZoneStatus][]coreEntity.ZoneStatus{
		coreEntity.ZoneStatusPlanned:     {coreEntity.ZoneStatusActive, coreEntity.ZoneStatusDisabled},
		coreEntity.ZoneStatusActive:      {coreEntity.ZoneStatusDraining, coreEntity.ZoneStatusMaintenance, coreEntity.ZoneStatusDisabled},
		coreEntity.ZoneStatusDraining:    {coreEntity.ZoneStatusActive, coreEntity.ZoneStatusMaintenance, coreEntity.ZoneStatusDisabled},
		coreEntity.ZoneStatusMaintenance: {coreEntity.ZoneStatusActive, coreEntity.ZoneStatusDisabled},
		coreEntity.ZoneStatusDisabled:    {coreEntity.ZoneStatusActive},
	}

	allowedOld := append(allowed[toStatus], toStatus)
	updatedZone, err := s.repo.UpdateZoneStatus(ctx, zoneID, toStatus, allowedOld)
	if err != nil {
		return nil, err
	}

	if ok := s.l1Registry.InvalidateLocal(ctx, "zone_catalog:"); ok {
		coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateSuccess)
	} else {
		coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateFailed)
	}

	detachedCtx := context.WithoutCancel(ctx)
	go func() {
		if _, err := s.l1Fanout.Publish(detachedCtx, "zone_catalog:", nil); err != nil {
			coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
		} else {
			coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
		}
	}()

	return updatedZone, nil
}

// DeleteZone xóa zone khi đủ 3 preconditions.
func (s *ZoneService) DeleteZone(ctx context.Context, zoneID uuid.UUID) error {
	code, err := s.repo.DeleteZone(ctx, zoneID)
	if err != nil {
		return err
	}

	if ok := s.l1Registry.InvalidateLocal(ctx, "zone_catalog:"); ok {
		coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateSuccess)
	} else {
		coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateFailed)
	}
	if ok := s.l1Registry.InvalidateLocal(ctx, "zone_by_code:"+code); ok {
		coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateSuccess)
	} else {
		coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateFailed)
	}

	detachedCtx := context.WithoutCancel(ctx)
	go func() {
		if _, err := s.l1Fanout.Publish(detachedCtx, "zone_catalog:", nil); err != nil {
			coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
		} else {
			coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
		}
	}()
	go func() {
		if _, err := s.l1Fanout.Publish(detachedCtx, "zone_by_code:"+code, nil); err != nil {
			coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
		} else {
			coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
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

	if ok := s.l1Registry.InvalidateLocal(ctx, "zone_catalog:"); ok {
		coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateSuccess)
	} else {
		coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateFailed)
	}
	if ok := s.l1Registry.InvalidateLocal(ctx, "zone_by_code:"+zoneCode); ok {
		coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateSuccess)
	} else {
		coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1InvalidateFailed)
	}

	detachedCtx := context.WithoutCancel(ctx)
	go func() {
		if _, err := s.l1Fanout.Publish(detachedCtx, "zone_catalog:", nil); err != nil {
			coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
		} else {
			coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
		}
	}()
	go func() {
		if _, err := s.l1Fanout.Publish(detachedCtx, "zone_by_code:"+zoneCode, nil); err != nil {
			coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutFailed)
		} else {
			coreMetric.ObserveZoneOperation("cache", coreTaxonomy.OutcomeL1FanoutSuccess)
		}
	}()

	return svc, nil
}
