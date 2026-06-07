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
	"strings"
	"time"

	"controlplane/internal/cacheengine"
	coreCache "controlplane/internal/core/cache"
	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreSvcInterface "controlplane/internal/core/domain/service"
	coreErrorx "controlplane/internal/core/errorx"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

type ZoneService struct {
	repo       coreRepoInterface.ZoneRepository
	cache      *coreCache.ZoneFanoutCache // L2 RAM cache — COW, lock-free reads, versioned
	l1Registry *cacheengine.CacheRegistry // Tích hợp CacheRegistry từ cache-engine
	l1Fanout   *cacheengine.RedisFanout   // Tích hợp RedisFanout độc lập
	sfg        singleflight.Group
}

func NewZoneService(
	repo coreRepoInterface.ZoneRepository,
	cache *coreCache.ZoneFanoutCache,
	l1Registry *cacheengine.CacheRegistry,
	l1Fanout *cacheengine.RedisFanout,
) coreSvcInterface.ZoneService {
	return &ZoneService{
		repo:       repo,
		cache:      cache,
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
	if s.l1Registry == nil {
		// SRE Warning: Nếu registry chưa được cấu hình (ví dụ trong môi trường test),
		// thực hiện fallback gọi trực tiếp repo từ DB.
		return s.repo.GetZoneCatalog(ctx)
	}

	val, err := s.l1Registry.GetOrLoad(ctx, "zone_catalog", "")
	if err != nil {
		return nil, err
	}

	// Ép kiểu trực tiếp (Type Assertion) từ raw interface{} nhận được từ CacheRegistry.
	// Cơ chế này giúp đạt hiệu năng O(1) và Zero-Allocation trên RAM (không tốn CPU parse JSON).
	catalog, ok := val.([]coreEntity.ZoneCatalog)
	if !ok {
		// SRE HA Warning: Dữ liệu trong cache bị lỗi kiểu cấu trúc (type mismatch),
		// thực hiện fallback về database để đảm bảo hệ thống không bị lỗi panic/crash.
		return s.repo.GetZoneCatalog(ctx)
	}

	return catalog, nil
}

// CreateZone tạo zone mới + cập nhật cache (tự động đồng bộ các replica).
func (s *ZoneService) CreateZone(ctx context.Context, input coreEntity.CreateZoneInput) error {
	now := time.Now().UTC()
	zoneID, err := uuid.NewV7()
	if err != nil {
		return err
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

	s.cache.PatchZone(ctx, zone, zone.UpdatedAt.Unix())

	if s.l1Registry != nil {
		s.l1Registry.InvalidateLocal(ctx, "zone_catalog:")
	}
	if s.l1Fanout != nil {
		_, _ = s.l1Fanout.Publish(ctx, "zone_catalog:", nil)
	}

	return nil
}

// GetZoneDetailByID lấy thông tin chi tiết của một Zone kèm theo tất cả các dịch vụ của nó.
func (s *ZoneService) GetZoneDetailByID(ctx context.Context, id uuid.UUID) (*coreEntity.ZoneDetail, error) {
	detail, err := s.repo.GetZoneDetailByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, coreErrorx.ErrZoneNotFound
	}
	return detail, nil
}

// GetZoneIDByCode lấy zone ID từ zone code. Hàm này dùng để nạp cache.
func (s *ZoneService) GetZoneIDByCode(ctx context.Context, code string) (uuid.UUID, error) {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		return uuid.Nil, coreErrorx.ErrZoneNotFound
	}
	if s.cache != nil {
		if z, ok := s.cache.GetByCode(code); ok {
			return z.ID, nil
		}
	}
	// Fallback to query database with singleflight coalescing to avoid thundering herd
	v, err, _ := s.sfg.Do("zone:code:"+code, func() (any, error) {
		// Re-check cache inside singleflight block in case a concurrent request already populated it
		if s.cache != nil {
			if z, ok := s.cache.GetByCode(code); ok {
				return z.ID, nil
			}
		}

		zones, err := s.repo.ListZones(ctx)
		if err != nil {
			return uuid.Nil, err
		}
		for _, z := range zones {
			if strings.ToLower(z.Code) == code {
				if s.cache != nil {
					var v int64
					if z.UpdatedAt != nil {
						v = z.UpdatedAt.UnixNano()
					}
					s.cache.PatchZone(ctx, z, v)
				}
				return z.ID, nil
			}
		}
		return uuid.Nil, coreErrorx.ErrZoneNotFound
	})
	if err != nil {
		return uuid.Nil, err
	}
	return v.(uuid.UUID), nil
}

// UpdateZoneStatus chuyển trạng thái zone theo state machine.
func (s *ZoneService) UpdateZoneStatus(ctx context.Context, zoneID uuid.UUID, toStatus coreEntity.ZoneStatus) (*coreEntity.Zone, error) {
	zone, err := s.repo.GetZoneByID(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	if zone == nil {
		return nil, coreErrorx.ErrZoneNotFound
	}

	allowed := map[coreEntity.ZoneStatus][]coreEntity.ZoneStatus{
		coreEntity.ZoneStatusPlanned:     {coreEntity.ZoneStatusActive, coreEntity.ZoneStatusDisabled},
		coreEntity.ZoneStatusActive:      {coreEntity.ZoneStatusDraining, coreEntity.ZoneStatusMaintenance, coreEntity.ZoneStatusDisabled},
		coreEntity.ZoneStatusDraining:    {coreEntity.ZoneStatusActive, coreEntity.ZoneStatusMaintenance, coreEntity.ZoneStatusDisabled},
		coreEntity.ZoneStatusMaintenance: {coreEntity.ZoneStatusActive, coreEntity.ZoneStatusDisabled},
		coreEntity.ZoneStatusDisabled:    {coreEntity.ZoneStatusActive},
	}
	canTransit := zone.Status == toStatus
	if !canTransit {
		for _, s := range allowed[zone.Status] {
			if s == toStatus {
				canTransit = true
				break
			}
		}
	}
	if !canTransit {
		return nil, coreErrorx.ErrZoneInvalidTransition
	}

	if err := s.repo.UpdateZoneStatus(ctx, zoneID, toStatus); err != nil {
		return nil, err
	}

	updated, err := s.repo.GetZoneByID(ctx, zoneID)
	if err != nil {
		return nil, err
	}

	if updated != nil && s.cache != nil {
		var v int64
		if updated.UpdatedAt != nil {
			v = updated.UpdatedAt.UnixNano()
		}
		s.cache.PatchZone(ctx, *updated, v)
	}

	if s.l1Registry != nil {
		s.l1Registry.InvalidateLocal(ctx, "zone_catalog:")
	}
	if s.l1Fanout != nil {
		_, _ = s.l1Fanout.Publish(ctx, "zone_catalog:", nil)
	}

	return updated, nil
}

// DeleteZone xóa mềm zone khi đủ 3 preconditions.
func (s *ZoneService) DeleteZone(ctx context.Context, zoneID uuid.UUID) error {
	zone, err := s.repo.GetZoneByID(ctx, zoneID)
	if err != nil {
		return err
	}
	if zone == nil {
		return coreErrorx.ErrZoneNotFound
	}
	if zone.Status != coreEntity.ZoneStatusDisabled {
		return coreErrorx.ErrZoneDeletePreconditionFailed
	}
	hasNodes, err := s.repo.HasDataplaneNodesByZone(ctx, zoneID)
	if err != nil {
		return err
	}
	if hasNodes {
		return coreErrorx.ErrZoneDeletePreconditionFailed
	}
	hasEnabledSvc, err := s.repo.HasEnabledZoneServicesByZone(ctx, zoneID)
	if err != nil {
		return err
	}
	if hasEnabledSvc {
		return coreErrorx.ErrZoneDeletePreconditionFailed
	}

	if err := s.repo.DeleteZone(ctx, zoneID); err != nil {
		return err
	}

	if s.cache != nil {
		s.cache.EvictZone(ctx, zone.ID.String(), zone.Code)
	}

	if s.l1Registry != nil {
		s.l1Registry.InvalidateLocal(ctx, "zone_catalog:")
	}
	if s.l1Fanout != nil {
		_, _ = s.l1Fanout.Publish(ctx, "zone_catalog:", nil)
	}

	return nil
}

// ListZoneServices trả danh sách tất cả zone services của một zone.
func (s *ZoneService) ListZoneServices(ctx context.Context, zoneID uuid.UUID) ([]coreEntity.ZoneService, error) {
	zone, err := s.repo.GetZoneByID(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	if zone == nil {
		return nil, coreErrorx.ErrZoneServiceZoneNotFound
	}
	return s.repo.ListZoneServicesByZoneID(ctx, zoneID)
}

// UpsertZoneService cập nhật enabled/disabled của một service trong zone.
func (s *ZoneService) UpsertZoneService(ctx context.Context, zoneID uuid.UUID, serviceType coreEntity.ZoneServiceType, enabled bool) (*coreEntity.ZoneService, error) {
	zone, err := s.repo.GetZoneByID(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	if zone == nil {
		return nil, coreErrorx.ErrZoneServiceZoneNotFound
	}
	if zone.Status != coreEntity.ZoneStatusMaintenance {
		return nil, coreErrorx.ErrZoneServiceStateConflict
	}

	svc, err := s.repo.UpsertZoneServiceByZoneAndType(ctx, zoneID, serviceType, enabled)
	if err != nil {
		return nil, err
	}

	// Zone entity không thay đổi fields nhưng catalog cần refresh
	if s.cache != nil {
		s.cache.PatchZone(ctx, *zone, svc.UpdatedAt.UnixNano())
	}

	if s.l1Registry != nil {
		s.l1Registry.InvalidateLocal(ctx, "zone_catalog:")
	}
	if s.l1Fanout != nil {
		_, _ = s.l1Fanout.Publish(ctx, "zone_catalog:", nil)
	}

	return svc, nil
}
