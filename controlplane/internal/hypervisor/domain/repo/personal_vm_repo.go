package hypervisorRepoInterface

import (
	"context"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"

	"github.com/google/uuid"
)

type PersonalVMRepository interface {
	CreateOrGet(
		ctx context.Context,
		vm *hypervisorEntity.PersonalVM,
		outbox *hypervisorEntity.HypervisorOutboxRecord,
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
}
