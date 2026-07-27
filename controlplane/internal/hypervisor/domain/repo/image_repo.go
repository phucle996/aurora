package hypervisorRepoInterface

import (
	"context"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"

	"github.com/google/uuid"
)

type ImageRepository interface {
	RegisterImageMetadata(
		ctx context.Context,
		image *hypervisorEntity.ImageArtifact,
	) (*hypervisorEntity.ImageArtifact, error)
	ListAdmin(
		ctx context.Context,
		zoneID uuid.UUID,
		limit int32,
	) ([]*hypervisorEntity.ImageArtifact, error)
	ListCatalog(
		ctx context.Context,
		zoneID uuid.UUID,
	) ([]*hypervisorEntity.ImageArtifact, error)
	Get(
		ctx context.Context,
		imageID uuid.UUID,
		zoneID uuid.UUID,
	) (*hypervisorEntity.ImageArtifact, error)
	GetAvailable(
		ctx context.Context,
		imageID uuid.UUID,
		zoneID uuid.UUID,
	) (*hypervisorEntity.ImageArtifact, error)
	BeginImport(
		ctx context.Context,
		imageID uuid.UUID,
		zoneID uuid.UUID,
		outbox *hypervisorEntity.HypervisorOutboxRecord,
	) (*hypervisorEntity.ImageArtifact, error)
	BeginDelete(
		ctx context.Context,
		imageID uuid.UUID,
		zoneID uuid.UUID,
		outbox *hypervisorEntity.HypervisorOutboxRecord,
	) (*hypervisorEntity.ImageArtifact, error)
}
