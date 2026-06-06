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

	coreCache "controlplane/internal/core/cache"
	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreSvcInterface "controlplane/internal/core/domain/service"
	coreErrorx "controlplane/internal/core/errorx"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

type ZoneService struct {
	repo  coreRepoInterface.ZoneRepository
	cache *coreCache.ZoneFanoutCache // L2 RAM cache — COW, lock-free reads, versioned
	sfg   singleflight.Group
}

type catalogWithVersion struct {
	catalog []coreEntity.ZoneCatalog
	version int64
}

func NewZoneService(
	repo coreRepoInterface.ZoneRepository,
	cache *coreCache.ZoneFanoutCache,
) coreSvcInterface.ZoneService {
	return &ZoneService{repo: repo, cache: cache}
}

func (s *ZoneService) ListZones(ctx context.Context) ([]coreEntity.Zone, error) {
	return s.repo.ListZones(ctx)
}

// GetZoneCatalog hot-path: L2 RAM cache O(1), singleflight coalesces DB misses.
func (s *ZoneService) GetZoneCatalog(ctx context.Context) ([]coreEntity.ZoneCatalog, error) {
	if catalog, ok := s.cache.GetCatalog(); ok {
		return catalog, nil
	}

	// Cache miss: singleflight gom tất cả concurrent req thành 1 DB call
	v, err, _ := s.sfg.Do("zone:catalog", func() (any, error) {
		catalog, err := s.repo.GetZoneCatalog(ctx)
		if err != nil {
			return nil, err
		}
		var maxVersion int64
		for _, item := range catalog {
			if item.UpdatedAt != nil {
				unixNano := item.UpdatedAt.UnixNano()
				if unixNano > maxVersion {
					maxVersion = unixNano
				}
			}
		}
		return &catalogWithVersion{catalog: catalog, version: maxVersion}, nil
	})
	if err != nil {
		return nil, err
	}
	result := v.(*catalogWithVersion)
	s.cache.SetCatalog(result.catalog, result.version)
	return result.catalog, nil
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

	return nil
}

// GetZoneByID lấy thông tin chi tiết của một Zone.
func (s *ZoneService) GetZoneByID(ctx context.Context, id uuid.UUID) (*coreEntity.Zone, error) {
	zone, err := s.repo.GetZoneByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if zone == nil {
		return nil, coreErrorx.ErrZoneNotFound
	}
	return zone, nil
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


// GetZoneByCode lấy thông tin chi tiết của một Zone bằng mã zone code.
func (s *ZoneService) GetZoneByCode(ctx context.Context, code string) (*coreEntity.Zone, error) {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		return nil, coreErrorx.ErrZoneNotFound
	}
	if s.cache != nil {
		if z, ok := s.cache.GetByCode(code); ok {
			return &z, nil
		}
	}
	// Fallback to query database with singleflight coalescing to avoid thundering herd
	v, err, _ := s.sfg.Do("zone:code:"+code, func() (any, error) {
		// Re-check cache inside singleflight block in case a concurrent request already populated it
		if s.cache != nil {
			if z, ok := s.cache.GetByCode(code); ok {
				return &z, nil
			}
		}

		zones, err := s.repo.ListZones(ctx)
		if err != nil {
			return nil, err
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
				return &z, nil
			}
		}
		return nil, coreErrorx.ErrZoneNotFound
	})
	if err != nil {
		return nil, err
	}
	return v.(*coreEntity.Zone), nil
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
	return svc, nil
}
