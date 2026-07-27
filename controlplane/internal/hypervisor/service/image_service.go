package hypervisorSvcImpl

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
	hypervisorproto "controlplane/internal/hypervisor/transport/rpc/proto"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

type ImageServiceImpl struct {
	repo hypervisorRepoInterface.ImageRepository
}

func NewImageService(
	repo hypervisorRepoInterface.ImageRepository,
) hypervisorSvcInterface.ImageService {
	return &ImageServiceImpl{repo: repo}
}

func (s *ImageServiceImpl) RegisterImageMetadata(
	ctx context.Context,
	input *hypervisorEntity.RegisterImageMetadata,
) (*hypervisorEntity.ImageArtifact, error) {
	imageID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	image := &hypervisorEntity.ImageArtifact{
		ID:           imageID,
		ZoneID:       input.ZoneID,
		Name:         input.Name,
		Code:         input.Code,
		Distribution: input.Distribution,
		Release:      input.Release,
		Revision:     input.Revision,
		Architecture: input.Architecture,
		Format:       input.Format,
		SizeBytes:    input.SizeBytes,
		SHA256:       input.SHA256,
		// The object key is derived from immutable identifiers. Neither the
		// browser nor a Zone may choose an arbitrary MinIO path.
		ObjectKey: fmt.Sprintf(
			"images/%s/revisions/%d/%s.%s",
			imageID.String(),
			input.Revision,
			hex.EncodeToString(input.SHA256),
			input.Format,
		),
		State:     hypervisorEntity.ImageStateUploading,
		CreatedBy: input.CreatedBy,
		CreatedAt: now,
		UpdatedAt: now,
	}
	return s.repo.RegisterImageMetadata(ctx, image)
}

func (s *ImageServiceImpl) ListAdmin(
	ctx context.Context,
	zoneID uuid.UUID,
	limit int32,
) ([]*hypervisorEntity.ImageArtifact, error) {
	return s.repo.ListAdmin(ctx, zoneID, limit)
}

func (s *ImageServiceImpl) ListCatalog(
	ctx context.Context,
	zoneID uuid.UUID,
) ([]*hypervisorEntity.ImageArtifact, error) {
	return s.repo.ListCatalog(ctx, zoneID)
}

func (s *ImageServiceImpl) BeginImport(
	ctx context.Context,
	input *hypervisorEntity.ImageImportRequest,
) (*hypervisorEntity.ImageArtifact, error) {
	image, err := s.repo.Get(ctx, input.ImageID, input.ZoneID)
	if err != nil {
		return nil, err
	}
	payload, err := proto.Marshal(&hypervisorproto.ImageImportV1{
		SchemaVersion: 1,
		ImageId:       image.ID[:],
		ZoneId:        image.ZoneID[:],
		Revision:      uint64(image.Revision),
		ObjectKey:     image.ObjectKey,
		Format:        image.Format,
		Architecture:  image.Architecture,
		SizeBytes:     uint64(image.SizeBytes),
		Sha256:        image.SHA256,
	})
	if err != nil {
		return nil, err
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		id := spanContext.TraceID()
		traceID = id[:]
	}
	return s.repo.BeginImport(ctx, image.ID, image.ZoneID, &hypervisorEntity.HypervisorOutboxRecord{
		EventID:              eventID,
		ZoneID:               image.ZoneID,
		JobTopic:             "hypervisor.image.import",
		Payload:              payload,
		Status:               "PENDING",
		JobVersion:           1,
		ResourceID:           image.ID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		IdleSeconds:          3600,
	})
}

func (s *ImageServiceImpl) BeginDelete(
	ctx context.Context,
	input *hypervisorEntity.ImageDeleteRequest,
) (*hypervisorEntity.ImageArtifact, error) {
	image, err := s.repo.Get(ctx, input.ImageID, input.ZoneID)
	if err != nil {
		return nil, err
	}
	var providerTemplateVMID uint64
	if image.ProviderTemplateVMID != nil {
		providerTemplateVMID = uint64(*image.ProviderTemplateVMID)
	}
	payload, err := proto.Marshal(&hypervisorproto.ImageDeleteV1{
		SchemaVersion:        1,
		ImageId:              image.ID[:],
		ZoneId:               image.ZoneID[:],
		Revision:             uint64(image.Revision),
		Sha256:               image.SHA256,
		ObjectKey:            image.ObjectKey,
		ProviderTemplateVmid: providerTemplateVMID,
	})
	if err != nil {
		return nil, err
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		id := spanContext.TraceID()
		traceID = id[:]
	}
	return s.repo.BeginDelete(ctx, image.ID, image.ZoneID, &hypervisorEntity.HypervisorOutboxRecord{
		EventID:              eventID,
		ZoneID:               image.ZoneID,
		JobTopic:             "hypervisor.image.delete",
		Payload:              payload,
		Status:               "PENDING",
		JobVersion:           1,
		ResourceID:           image.ID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		IdleSeconds:          1800,
	})
}
