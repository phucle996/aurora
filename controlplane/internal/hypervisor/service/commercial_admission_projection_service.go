package hypervisorSvcImpl

import (
	"context"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
)

// HypervisorCommercialAdmissionProjectionService là Domain Service thực thi việc cập nhật và duy trì
// bản chiếu (Local Projection) về quyền hạn thương mại của các chủ sở hữu tài nguyên (Personal / Tenant)
// ngay bên trong cơ sở dữ liệu nội bộ của Hypervisor Controlplane.
type HypervisorCommercialAdmissionProjectionService struct {
	repo hypervisorRepoInterface.CommercialAdmissionProjectionRepository
}

// NewHypervisorCommercialAdmissionProjectionService khởi tạo một instance mới của HypervisorCommercialAdmissionProjectionService.
func NewHypervisorCommercialAdmissionProjectionService(
	repo hypervisorRepoInterface.CommercialAdmissionProjectionRepository,
) hypervisorSvcInterface.CommercialAdmissionProjectionService {
	return &HypervisorCommercialAdmissionProjectionService{
		repo: repo,
	}
}

// Apply ghi nhận quyết định cấp phép đã được xác thực ở tầng transport vào bảng chiếu cơ sở dữ liệu.
func (s *HypervisorCommercialAdmissionProjectionService) Apply(
	ctx context.Context,
	command *hypervisorEntity.CommercialAdmissionProjectionCommand,
) error {
	var reason *string
	if command.RestrictionReason != "" {
		reason = &command.RestrictionReason
	}

	return s.repo.Upsert(ctx, &hypervisorEntity.CommercialAdmissionProjection{
		EventID:           command.EventID,
		OwnerID:           command.OwnerID,
		OwnerType:         command.OwnerType,
		PolicyVersion:     command.PolicyVersion,
		Decision:          command.Decision,
		RestrictionReason: reason,
		EffectiveAt:       command.EffectiveAt,
		ValidUntil:        command.ValidUntil,
	})
}
