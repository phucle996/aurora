package hypervisorSvcInterface

import (
	"context"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"

	"github.com/google/uuid"
)

type ImageService interface {
	RegisterImageMetadata(
		ctx context.Context,
		input *hypervisorEntity.RegisterImageMetadata,
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
	BeginImport(
		ctx context.Context,
		input *hypervisorEntity.ImageImportRequest,
	) (*hypervisorEntity.ImageArtifact, error)
	BeginDelete(
		ctx context.Context,
		input *hypervisorEntity.ImageDeleteRequest,
	) (*hypervisorEntity.ImageArtifact, error)
}
