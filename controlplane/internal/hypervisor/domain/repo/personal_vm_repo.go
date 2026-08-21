package hypervisorRepoInterface

import (
	"context"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"

	"github.com/google/uuid"
)

type PersonalVMRepository interface {
	GetAvailableImage(
		ctx context.Context,
		imageID uuid.UUID,
		zoneID uuid.UUID,
	) (*hypervisorEntity.ImageArtifact, error)
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
	GetDeleteTarget(
		ctx context.Context,
		vmID uuid.UUID,
		workspaceID uuid.UUID,
		ownerUserID uuid.UUID,
	) (*hypervisorEntity.PersonalVMDeleteTarget, error)
	BeginDelete(
		ctx context.Context,
		command *hypervisorEntity.BeginPersonalVMDelete,
	) (*hypervisorEntity.PersonalVMDeleteResult, error)
}
