package hypervisorSvcImpl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
	hypervisorTaxonomy "controlplane/internal/hypervisor/taxonomy"
	hypervisorproto "controlplane/internal/hypervisor/transport/rpc/proto"
	"controlplane/internal/observability"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

type PersonalVMServiceImpl struct {
	repo    hypervisorRepoInterface.PersonalVMRepository
	metrics observability.WorkflowRecorder
}

func NewPersonalVMService(
	repo hypervisorRepoInterface.PersonalVMRepository,
	metrics observability.WorkflowRecorder,
) hypervisorSvcInterface.PersonalVMService {
	return &PersonalVMServiceImpl{repo: repo, metrics: metrics}
}

func (s *PersonalVMServiceImpl) Create(
	ctx context.Context,
	input *hypervisorEntity.CreatePersonalVM,
) (out *hypervisorEntity.PersonalVMCreateResult, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, hypervisorTaxonomy.ErrNameConflict):
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		case errors.Is(err, hypervisorTaxonomy.ErrImageNotFound), errors.Is(err, hypervisorTaxonomy.ErrNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, hypervisorTaxonomy.ErrImageStateConflict), errors.Is(err, hypervisorTaxonomy.ErrScopeUnavailable):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	image, err := s.repo.GetAvailableImage(ctx, input.ImageID, input.ZoneID)
	if err != nil {
		return nil, err
	}
	vmID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	operationID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}

	spec := sha256.New()
	spec.Write(image.ID[:])
	var number [8]byte
	binary.BigEndian.PutUint64(number[:], uint64(image.Revision))
	spec.Write(number[:])
	spec.Write(image.SHA256)
	binary.BigEndian.PutUint64(number[:], uint64(input.CPUCores))
	spec.Write(number[:])
	binary.BigEndian.PutUint64(number[:], uint64(input.MemoryMB))
	spec.Write(number[:])
	binary.BigEndian.PutUint64(number[:], uint64(input.DiskGB))
	spec.Write(number[:])
	spec.Write([]byte(input.SSHPublicKey))
	specHash := spec.Sum(nil)

	now := time.Now().UTC()
	providerName := "aurora-" + vmID.String()
	vm := &hypervisorEntity.PersonalVM{
		ID:            vmID,
		WorkspaceID:   input.WorkspaceID,
		ZoneID:        input.ZoneID,
		OwnerUserID:   input.OwnerUserID,
		Name:          input.Name,
		Image:         image.Name,
		ImageID:       &image.ID,
		ImageRevision: &image.Revision,
		ImageSHA256:   image.SHA256,
		CPUCores:      input.CPUCores,
		MemoryMB:      input.MemoryMB,
		DiskGB:        input.DiskGB,
		SSHPublicKey:  input.SSHPublicKey,
		SpecHash:      specHash,
		Status:        hypervisorEntity.VMStatusProvisioning,
		OperationID:   operationID,
		ProviderName:  providerName,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	payload, err := proto.Marshal(&hypervisorproto.VmCreateV1{
		SchemaVersion:        1,
		VmId:                 vmID[:],
		ProviderName:         providerName,
		ImageId:              image.ID[:],
		CpuCores:             uint32(input.CPUCores),
		MemoryMb:             uint64(input.MemoryMB),
		DiskGb:               uint64(input.DiskGB),
		SshPublicKey:         input.SSHPublicKey,
		ConfigHash:           specHash,
		ImageRevision:        uint64(image.Revision),
		ImageSha256:          image.SHA256,
		ProviderTemplateVmid: uint64(*image.ProviderTemplateVMID),
	})
	if err != nil {
		return nil, err
	}

	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		id := spanContext.TraceID()
		traceID = id[:]
	}
	outbox := &hypervisorEntity.HypervisorOutboxRecord{
		EventID:              operationID,
		ZoneID:               input.ZoneID,
		JobTopic:             "hypervisor.vm.create",
		Payload:              payload,
		ActorUserID:          &input.OwnerUserID,
		Status:               "PENDING",
		JobVersion:           1,
		ResourceID:           vmID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		IdleSeconds:          600,
	}

	result, err := s.repo.CreateOrGet(ctx, vm, outbox)
	if err != nil {
		return nil, err
	}
	if !result.Created && !bytes.Equal(result.VM.SpecHash, specHash) {
		return nil, hypervisorTaxonomy.ErrNameConflict
	}
	return result, nil
}

func (s *PersonalVMServiceImpl) List(
	ctx context.Context,
	workspaceID uuid.UUID,
	zoneID uuid.UUID,
	ownerUserID uuid.UUID,
	limit int32,
) (out []*hypervisorEntity.PersonalVM, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.List(ctx, workspaceID, zoneID, ownerUserID, limit)
}

func (s *PersonalVMServiceImpl) Get(
	ctx context.Context,
	vmID uuid.UUID,
	workspaceID uuid.UUID,
	ownerUserID uuid.UUID,
) (out *hypervisorEntity.PersonalVM, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, hypervisorTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.Get(ctx, vmID, workspaceID, ownerUserID)
}
