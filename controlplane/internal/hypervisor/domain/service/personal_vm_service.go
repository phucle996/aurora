package hypervisorSvcInterface

import (
	"context"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"

	"github.com/google/uuid"
)

type PersonalVMService interface {
	Create(
		ctx context.Context,
		input *hypervisorEntity.CreatePersonalVM,
	) (*hypervisorEntity.PersonalVMCreateResult, error)
	List(
		ctx context.Context,
		workspaceID uuid.UUID,
		zoneID uuid.UUID,
		ownerUserID uuid.UUID,
		limit int32,
	) ([]*hypervisorEntity.PersonalVM, error)
	Get(
		ctx context.Context,
		vmID uuid.UUID,
		workspaceID uuid.UUID,
		ownerUserID uuid.UUID,
	) (*hypervisorEntity.PersonalVM, error)
	Delete(
		ctx context.Context,
		vmID uuid.UUID,
		workspaceID uuid.UUID,
		zoneID uuid.UUID,
		ownerUserID uuid.UUID,
	) (*hypervisorEntity.PersonalVMDeleteResult, error)
}
