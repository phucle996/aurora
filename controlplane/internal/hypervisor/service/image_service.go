package hypervisorSvcImpl

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
	hypervisorTaxonomy "controlplane/internal/hypervisor/taxonomy"
	hypervisorproto "controlplane/internal/hypervisor/transport/proto"
	"controlplane/internal/observability"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

// ImageServiceImpl triển khai Domain Service ImageService cho Hypervisor Controlplane.
// Quản lý toàn bộ vòng đời OS Image Artifact từ đăng ký metadata, import template đến xóa hạ tầng.
type ImageServiceImpl struct {
	repo    hypervisorRepoInterface.ImageRepository
	metrics observability.WorkflowRecorder
}

// NewImageService khởi tạo một instance mới của ImageServiceImpl.
func NewImageService(
	repo hypervisorRepoInterface.ImageRepository,
	metrics observability.WorkflowRecorder,
) hypervisorSvcInterface.ImageService {
	return &ImageServiceImpl{
		repo:    repo,
		metrics: metrics,
	}
}

// RegisterImageMetadata khởi tạo bản ghi Image Artifact mới ở trạng thái UPLOADING và tạo đường dẫn ObjectKey bất biến trên MinIO.
func (s *ImageServiceImpl) RegisterImageMetadata(
	ctx context.Context,
	input *hypervisorEntity.RegisterImageMetadata,
) (out *hypervisorEntity.ImageArtifact, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, hypervisorTaxonomy.ErrImageConflict):
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		case errors.Is(err, hypervisorTaxonomy.ErrScopeUnavailable):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	// [COMMENT]: 1. Sinh UUIDv7 cho ImageID để đảm bảo tính tuần tự theo thời gian
	imageID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()

	// [COMMENT]: 2. Đường dẫn ObjectKey được sinh cố định từ các định danh bất biến (Immutable Identifiers).
	// Cả browser và Zone đều không được tự ý chọn đường dẫn tùy ý trên MinIO.
	objectKey := fmt.Sprintf(
		"images/%s/revisions/%d/%s.%s",
		imageID.String(),
		input.Revision,
		hex.EncodeToString(input.SHA256),
		input.Format,
	)

	// [COMMENT]: 3. Khởi tạo đối tượng ImageArtifact ở trạng thái UPLOADING ban đầu
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
		ObjectKey:    objectKey,
		State:        hypervisorEntity.ImageStateUploading,
		CreatedBy:    input.CreatedBy,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	return s.repo.RegisterImageMetadata(ctx, image)
}

// ListAdmin truy vấn danh sách tất cả các Image Artifact trong một Zone cho giao diện SRE Admin.
func (s *ImageServiceImpl) ListAdmin(
	ctx context.Context,
	zoneID uuid.UUID,
	limit int32,
) (out []*hypervisorEntity.ImageArtifact, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	return s.repo.ListAdmin(ctx, zoneID, limit)
}

// ListCatalog truy vấn danh mục OS Images đang ở trạng thái AVAILABLE để người dùng lựa chọn khi tạo VM.
func (s *ImageServiceImpl) ListCatalog(
	ctx context.Context,
	zoneID uuid.UUID,
) (out []*hypervisorEntity.ImageArtifact, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	return s.repo.ListCatalog(ctx, zoneID)
}

// BeginImport kích hoạt quy trình Import chuyển đổi file nhị phân trên MinIO thành Proxmox VM Template.
func (s *ImageServiceImpl) BeginImport(
	ctx context.Context,
	input *hypervisorEntity.ImageImportRequest,
) (out *hypervisorEntity.ImageArtifact, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, hypervisorTaxonomy.ErrImageNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, hypervisorTaxonomy.ErrImageStateConflict):
			result, reason = observability.ResultRejected, observability.ReasonInvalidTransition
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	// [COMMENT]: 1. Đọc thông tin hiện tại của Image từ repository
	image, err := s.repo.Get(ctx, input.ImageID, input.ZoneID)
	if err != nil {
		return nil, err
	}

	// [COMMENT]: 2. Đóng gói Protobuf payload cho job import template
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

	// [COMMENT]: 3. Sinh EventID và trích xuất OpenTelemetry TraceID nếu có
	eventID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}

	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		id := spanContext.TraceID()
		traceID = id[:]
	}

	// [COMMENT]: 4. Thực thi CTE chuyển trạng thái sang IMPORTING và ghi Outbox record
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

// BeginDelete kích hoạt quy trình xóa Image Artifact khỏi Zone và thu hồi tài nguyên template trên hạ tầng.
func (s *ImageServiceImpl) BeginDelete(
	ctx context.Context,
	input *hypervisorEntity.ImageDeleteRequest,
) (out *hypervisorEntity.ImageArtifact, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, hypervisorTaxonomy.ErrImageNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, hypervisorTaxonomy.ErrImageStateConflict):
			result, reason = observability.ResultRejected, observability.ReasonInvalidTransition
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	// [COMMENT]: 1. Đọc thông tin hiện tại của Image từ repository
	image, err := s.repo.Get(ctx, input.ImageID, input.ZoneID)
	if err != nil {
		return nil, err
	}

	var providerTemplateVMID uint64
	if image.ProviderTemplateVMID != nil {
		providerTemplateVMID = uint64(*image.ProviderTemplateVMID)
	}

	// [COMMENT]: 2. Đóng gói Protobuf payload cho job xóa image template và storage
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

	// [COMMENT]: 3. Sinh EventID và trích xuất OpenTelemetry TraceID nếu có
	eventID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}

	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		id := spanContext.TraceID()
		traceID = id[:]
	}

	// [COMMENT]: 4. Thực thi CTE chuyển trạng thái sang DELETING và ghi Outbox record
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
