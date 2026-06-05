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
	if s.cache != nil {
		if catalog, ok := s.cache.GetCatalog(); ok {
			return catalog, nil
		}

		// Cache miss: singleflight gom tất cả concurrent req thành 1 DB call
		v, err, _ := s.sfg.Do("zone:catalog", func() (any, error) {
			catalog, err := s.repo.GetZoneCatalog(ctx)
			if err != nil {
				return nil, err
			}
			// Đọc version hiện tại từ Redis qua cache interface
			version := s.cache.GetVersion(ctx)
			return &catalogWithVersion{catalog: catalog, version: version}, nil
		})
		if err != nil {
			return nil, err
		}
		result := v.(*catalogWithVersion)
		s.cache.SetCatalog(result.catalog, result.version)
		return result.catalog, nil
	}
	return s.repo.GetZoneCatalog(ctx)
}

// CreateZone tạo zone mới + cập nhật cache (tự động đồng bộ các replica).
func (s *ZoneService) CreateZone(ctx context.Context, input coreEntity.CreateZoneInput) error {
	now := time.Now().UTC()
	zoneID, err := uuid.NewV7()
	if err != nil {
		zoneID = uuid.New()
	}
	zone := coreEntity.Zone{
		ID:        zoneID.String(),
		Code:      strings.ToLower(strings.TrimSpace(input.Code)),
		Name:      strings.TrimSpace(input.Name),
		Status:    coreEntity.ZoneStatusPlanned,
		CreatedAt: now,
		UpdatedAt: now,
	}
	svcs := map[coreEntity.ZoneServiceType]bool{
		coreEntity.ZoneServiceTypeHypervisor: input.EnableHypervisor,
		coreEntity.ZoneServiceTypeStorage:    input.EnableStorage,
		coreEntity.ZoneServiceTypeMail:       input.EnableMail,
		coreEntity.ZoneServiceTypeK8s:        input.EnableK8s,
		coreEntity.ZoneServiceTypeAI:         input.EnableAI,
		coreEntity.ZoneServiceTypeDatabase:   false,
	}

	if err := s.repo.CreateZone(ctx, zone, svcs); err != nil {
		return err
	}

	// Đồng bộ qua cache
	if s.cache != nil {
		s.cache.PatchZone(ctx, zone)
	}
	return nil
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
		s.cache.PatchZone(ctx, *updated)
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
		s.cache.EvictZone(ctx, zone.ID, zone.Code)
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
		s.cache.PatchZone(ctx, *zone)
	}
	return svc, nil
}
