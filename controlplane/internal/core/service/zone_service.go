// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/service/zone_service.go
//            Đặc Tả Nghiệp Vụ Quản Lý Vòng Đời Zone & Zone Services
// ======================================================================================================
//
// ⚠️  ZONE LÀ ROOT TOPOLOGY — THẬN TRỌNG KHI THAY ĐỔI
// ─────────────────────────────────────────────────────
//   Zone là đơn vị hạ tầng gốc (root topology unit) của toàn bộ hệ thống. Mọi thực thể vận hành
//   đều được neo vào một zone cụ thể:
//     - Dataplane nodes đăng ký và hoạt động dưới một zone.
//     - Zone services (hypervisor, storage, mail, k8s, ai) xác định năng lực của zone đó.
//     - Các tài nguyên cấp cao hơn (VM, volume, workload) sẽ được phân bổ dựa trên zone.
//
//   Hệ quả: bất kỳ thay đổi nào đến zone — đặc biệt là xóa, chuyển trạng thái, hoặc thay đổi
//   service config — đều có thể gây hiệu ứng lan rộng (blast radius) đến toàn bộ workload đang
//   chạy trên zone đó. Không thực hiện thay đổi zone trên production mà không có runbook rõ ràng.
//
// 📜 HIỆP ĐỒNG THIẾT KẾ (DESIGN CONTRACT):
//   - ZoneService là tầng business logic duy nhất chịu trách nhiệm điều phối toàn bộ vòng đời của
//     một Zone trong hệ thống: tạo mới, chuyển trạng thái, xóa, và quản lý các service con.
//
//   - Phân chia trách nhiệm rõ ràng theo layer:
//     * Handler (transport layer): chịu trách nhiệm validate toàn bộ input từ client — uuid parse,
//       status enum, service type enum, binding required. Không để service làm lại.
//     * Service (business layer — file này): chỉ enforce business rules thuần — state machine
//       transition, delete preconditions, zone-must-be-maintenance constraint.
//     * Repository (data layer): SoT duy nhất cho trạng thái zone trong Postgres.
//
// 🎯 ZONE LIFECYCLE & STATE MACHINE:
//   - Zone mới tạo luôn bắt đầu ở trạng thái `planned` — không cho phép client set status khi tạo.
//   - Sơ đồ chuyển trạng thái hợp lệ (một chiều, không bypass):
//
//       planned ──────────────────────────────────────────► active
//           └──────────────────────────────────────────────► disabled
//
//       active ───────────────────────────────────────────► draining
//           ├─────────────────────────────────────────────► maintenance
//           └─────────────────────────────────────────────► disabled
//
//       draining ─────────────────────────────────────────► active
//           ├─────────────────────────────────────────────► maintenance
//           └─────────────────────────────────────────────► disabled
//
//       maintenance ──────────────────────────────────────► active
//           └─────────────────────────────────────────────► disabled
//
//       disabled ─────────────────────────────────────────► active
//
//   - Mọi transition không nằm trong sơ đồ trên đều bị từ chối với ErrZoneInvalidTransition.
//   - Transition từ một status sang chính nó (no-op) được cho phép để idempotent.
//
// 🔒 ZONE SERVICE (ENABLED SERVICES) CONTRACT:
//   - Khi tạo zone, tất cả 5 services (hypervisor, storage, mail, k8s, ai) được upsert đồng thời
//     trong cùng một luồng CreateZone — không tách thành call riêng.
//   - UpsertZoneService (cập nhật sau khi zone đã tồn tại) chỉ được phép khi zone đang ở trạng thái
//     `maintenance`. Đây là guard bảo vệ tính nhất quán dữ liệu — tránh thay đổi service config
//     khi zone đang phục vụ traffic thực.
//
// 🗑️ DELETE PRECONDITIONS (BẮT BUỘC ĐỦ 3 ĐIỀU KIỆN):
//   1. Zone phải ở trạng thái `disabled`.
//   2. Không còn dataplane node nào đang được gắn vào zone.
//   3. Không còn enabled service nào trong zone.
//   → Thiếu bất kỳ điều kiện nào → ErrZoneDeletePreconditionFailed.
//   → Mục đích: tránh orphan references trong dataplane và service registry.
//
// 🚀 LƯU Ý VẬN HÀNH:
//   - Service không tự log nghiệp vụ — mọi log do handler thực hiện.
//   - Mọi lỗi trả thẳng lên caller, không wrap thêm tầng.
//   - Dependency duy nhất là ZoneRepository — không phụ thuộc cache hay external service.
//   - App bootstrap (module.go) đảm bảo repo không nil trước khi service được khởi tạo;
//     service không tự kiểm tra nil repo tại runtime.
//
// 📞 CALLER:
//   - HTTP handler: `controlplane/internal/core/transport/http/handler/zone_handler.go`
//   - Module wiring: `controlplane/internal/core/module.go`
//
// ======================================================================================================

package coreSvcImpl

import (
	"context"
	"strings"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreSvcInterface "controlplane/internal/core/domain/service"
	coreErrorx "controlplane/internal/core/errorx"

	"github.com/google/uuid"
)

type ZoneService struct {
	repo coreRepoInterface.ZoneRepository
}

func NewZoneService(repo coreRepoInterface.ZoneRepository) coreSvcInterface.ZoneService {
	return &ZoneService{repo: repo}
}

func (s *ZoneService) ListZones(ctx context.Context) ([]coreEntity.Zone, error) {
	return s.repo.ListZones(ctx)
}

// CreateZone tạo zone mới với status cố định là `planned` và upsert toàn bộ 5 zone services
// trong cùng một luồng.
//
// Notes:
//   - Status luôn hardcode là ZoneStatusPlanned — client không được phép set status khi tạo.
//   - Tất cả 5 services (hypervisor, storage, mail, k8s, ai) đều được upsert ngay sau khi zone
//     được tạo thành công, kể cả khi enabled=false — đảm bảo bản ghi service luôn tồn tại đầy đủ.
//   - Nếu upsert bất kỳ service nào thất bại, zone đã được tạo nhưng service config có thể không
//     đầy đủ — caller cần xử lý lỗi và retry hoặc cleanup nếu cần.
func (s *ZoneService) CreateZone(ctx context.Context, input coreEntity.CreateZoneInput) error {
	now := time.Now().UTC()
	zoneID, zoneIDErr := uuid.NewV7()
	if zoneIDErr != nil {
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
	if err := s.repo.CreateZone(ctx, zone); err != nil {
		return err
	}

	svcs := map[coreEntity.ZoneServiceType]bool{
		coreEntity.ZoneServiceTypeHypervisor: input.EnableHypervisor,
		coreEntity.ZoneServiceTypeStorage:    input.EnableStorage,
		coreEntity.ZoneServiceTypeMail:       input.EnableMail,
		coreEntity.ZoneServiceTypeK8s:        input.EnableK8s,
		coreEntity.ZoneServiceTypeAI:         input.EnableAI,
	}
	for svcType, enabled := range svcs {
		if _, err := s.repo.UpsertZoneServiceByZoneAndType(ctx, zoneID, svcType, enabled); err != nil {
			return err
		}
	}

	return nil
}

// UpdateZoneStatus chuyển trạng thái zone theo state machine đã định nghĩa.
//
// Notes:
//   - Transition từ một status sang chính nó (no-op) được cho phép — idempotent safe.
//   - Mọi transition không hợp lệ trả ErrZoneInvalidTransition.
//   - Sau khi update thành công, trả về zone mới nhất từ DB (source of truth).
//   - Input validation (uuid parse, status enum) do handler thực hiện trước khi gọi vào đây.
func (s *ZoneService) UpdateZoneStatus(ctx context.Context, zoneID uuid.UUID, toStatus coreEntity.ZoneStatus) (*coreEntity.Zone, error) {
	zone, err := s.repo.GetZoneByID(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	if zone == nil {
		return nil, coreErrorx.ErrZoneNotFound
	}

	// State machine transition table — mọi chuyển trạng thái phải qua bảng này.
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
	return s.repo.GetZoneByID(ctx, zoneID)
}

// DeleteZone xóa zone khi đủ 3 preconditions: disabled + không còn dataplane node + không còn enabled service.
//
// Notes:
//   - Cả 3 điều kiện phải thỏa mãn đồng thời — thiếu bất kỳ điều kiện nào trả ErrZoneDeletePreconditionFailed.
//   - Mục đích guard này là tránh orphan references trong dataplane registry và service config.
//   - Thứ tự kiểm tra: status → nodes → services. Dừng sớm ở điều kiện đầu tiên thất bại.
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
	return s.repo.DeleteZone(ctx, zoneID)
}

// ListZoneServices trả danh sách tất cả zone services của một zone.
//
// Notes:
//   - Kiểm tra zone tồn tại trước khi query services để trả lỗi rõ ràng hơn là empty list.
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

// UpsertZoneService cập nhật trạng thái enabled/disabled của một service trong zone.
//
// Notes:
//   - Chỉ được phép khi zone đang ở trạng thái `maintenance` — guard tránh thay đổi service config
//     khi zone đang phục vụ traffic thực (active/draining).
//   - Operator phải chuyển zone sang maintenance trước, thực hiện thay đổi, rồi chuyển lại active.
//   - Input validation (service type enum, uuid parse) do handler thực hiện trước khi gọi vào đây.
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
	return s.repo.UpsertZoneServiceByZoneAndType(ctx, zoneID, serviceType, enabled)
}
