// ======================================================================================================
// 📂 MODULE: controlplane/internal/zone/service/zone_service.go
//            Đặc Tả Nghiệp Vụ Quản Lý Vòng Đời Zone & Zone Services
// ======================================================================================================
//
// 📜 THIẾT KẾ & TÁCH BIỆT TRÁCH NHIỆM:
//   - ZoneService chỉ tập trung vào logic nghiệp vụ và tương tác với Database (Source of Truth).
//   - Toàn bộ logic quản lý bộ nhớ đệm L2 RAM Cache, đồng bộ phiên bản (Versioning) và cơ chế phát tán
//     (Redis Pub/Sub Fanout) được che giấu hoàn toàn phía sau lớp cache (ZoneFanoutCache).
//
// ======================================================================================================

package zoneSvcImpl

import (
	"context"
	"time"

	coreEntity "controlplane/internal/zone/domain/entity"
	coreRepoInterface "controlplane/internal/zone/domain/repo"
	coreSvcInterface "controlplane/internal/zone/domain/service"
	coreMetric "controlplane/internal/zone/metrics"
	coreTaxonomy "controlplane/internal/zone/taxonomy"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
)

type ZoneService struct {
	repo coreRepoInterface.ZoneRepository
}

func NewZoneService(
	repo coreRepoInterface.ZoneRepository,
) coreSvcInterface.ZoneService {
	return &ZoneService{
		repo: repo,
	}
}

func (s *ZoneService) ListZones(ctx context.Context) ([]coreEntity.Zone, error) {
	// [COMMENT]: Gọi xuống repository ListZones và đo lường thời gian thực thi downstream
	start := time.Now()
	zones, err := s.repo.ListZones(ctx)
	duration := time.Since(start)
	if err != nil {
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "ListZones", coreMetric.OutcomeFailure, duration, err)
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, err
	}
	coreMetric.Downstream(ctx, coreMetric.KindRepo, "ListZones", coreMetric.OutcomeSuccess, duration, nil)
	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return zones, nil
}

// RPCListZones phục vụ luồng gRPC sync sang ACL chỉ lấy 4 thuộc tính (ID, Code, Name, Status).
// Triển khai này giúp tối ưu hóa hiệu năng, giảm dung lượng payload khi đồng bộ danh sách Zone qua biên.
func (s *ZoneService) RPCListZones(ctx context.Context) ([]coreEntity.RPCZone, error) {
	// [COMMENT]: Gọi xuống repository RPCListZones chuyên biệt và đo lường thời gian thực thi downstream
	start := time.Now()
	zones, err := s.repo.RPCListZones(ctx)
	duration := time.Since(start)
	if err != nil {
		// [COMMENT]: Ghi nhận lỗi truy vấn cơ sở dữ liệu kèm thời gian thực thi
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "RPCListZones", coreMetric.OutcomeFailure, duration, err)
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, err
	}
	// [COMMENT]: Ghi nhận truy vấn cơ sở dữ liệu thành công kèm thời gian thực thi
	coreMetric.Downstream(ctx, coreMetric.KindRepo, "RPCListZones", coreMetric.OutcomeSuccess, duration, nil)
	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return zones, nil
}

// CreateZone tạo zone mới + cập nhật cache (tự động đồng bộ các replica).
func (s *ZoneService) CreateZone(ctx context.Context, input coreEntity.CreateZoneInput) error {
	now := time.Now().UTC()
	zoneID, err := uuid.NewV7()
	if err != nil {
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return apperr.Wrap(coreTaxonomy.ErrZoneInvalidInput, err, coreMetric.OutcomeFailure)
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

	start := time.Now()
	if err := s.repo.CreateZone(ctx, zone, svcs); err != nil {
		duration := time.Since(start)
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "CreateZone", coreMetric.OutcomeFailure, duration, err)
		return err
	}

	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return nil
}

// GetZoneDetailByID lấy thông tin chi tiết của một Zone kèm theo tất cả các dịch vụ của nó.
func (s *ZoneService) GetZoneDetailByID(ctx context.Context, id uuid.UUID) (*coreEntity.ZoneDetail, error) {

	defer coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	start := time.Now()
	detail, err := s.repo.GetZoneDetailByID(ctx, id)
	if err != nil {
		duration := time.Since(start)
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "GetZoneDetailByID", coreMetric.OutcomeFailure, duration, err)
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
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return err
	}
	// todo : bắn rpc qua acr để update state zone đang cache
	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return nil
}

// DeleteZone xóa zone khi đủ 3 preconditions.
func (s *ZoneService) DeleteZone(ctx context.Context, zoneID uuid.UUID) error {
	_, err := s.repo.DeleteZone(ctx, zoneID)
	if err != nil {
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return err
	}
	// todo : bắn rpc qua acr để update state zone đang cache

	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return nil
}

// UpdateZoneService cập nhật enabled/disabled của một service trong zone.
func (s *ZoneService) UpdateZoneService(ctx context.Context, zoneID uuid.UUID, serviceType coreEntity.ZoneServiceType, enabled bool) (*coreEntity.ZoneService, error) {
	svc, _, err := s.repo.UpdateZoneService(ctx, zoneID, serviceType, enabled)
	if err != nil {
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, err
	}
	// todo : bắn rpc qua acr để update state zone đang cache

	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return svc, nil
}
