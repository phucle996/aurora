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
	hypervisorproto "controlplane/internal/hypervisor/transport/proto"
	"controlplane/internal/observability"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

type PersonalVMServiceImpl struct {
	repo      hypervisorRepoInterface.PersonalVMRepository
	admission hypervisorRepoInterface.CommercialAdmissionRepository
	pricing   hypervisorSvcInterface.PricingReadinessGate
	metrics   observability.WorkflowRecorder
}

func NewPersonalVMService(
	repo hypervisorRepoInterface.PersonalVMRepository,
	admission hypervisorRepoInterface.CommercialAdmissionRepository,
	pricing hypervisorSvcInterface.PricingReadinessGate,
	metrics observability.WorkflowRecorder,
) hypervisorSvcInterface.PersonalVMService {
	return &PersonalVMServiceImpl{repo: repo, admission: admission, pricing: pricing, metrics: metrics}
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
		case errors.Is(err, hypervisorTaxonomy.ErrCommercialAdmissionDenied):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		case errors.Is(err, hypervisorTaxonomy.ErrPricingUnavailable):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	if err := s.admission.RequirePersonalOwnerAdmission(ctx, input.OwnerUserID); err != nil {
		return nil, err
	}
	if err := s.pricing.RequireHypervisorPricing(ctx); err != nil {
		return nil, err
	}
	var cpuCores int32
	var memoryMB, bootDiskGB int64
	switch input.ResourceProfileCode {
	case "basic":
		cpuCores, memoryMB, bootDiskGB = 1, 2048, 32
	case "standard":
		cpuCores, memoryMB, bootDiskGB = 2, 4096, 64
	case "performance":
		cpuCores, memoryMB, bootDiskGB = 4, 8192, 128
	default:
		return nil, hypervisorTaxonomy.ErrInvalidResourceProfile
	}
	additionalDiskSizes := make([]int64, 0, len(input.AdditionalDisks))
	totalDiskGB := bootDiskGB
	protoDisks := make([]*hypervisorproto.VmCreateAdditionalDiskV1, 0, len(input.AdditionalDisks))
	for _, disk := range input.AdditionalDisks {
		totalDiskGB += disk.SizeGB
		additionalDiskSizes = append(additionalDiskSizes, disk.SizeGB)
		protoDisks = append(protoDisks, &hypervisorproto.VmCreateAdditionalDiskV1{DiskIndex: uint32(disk.DiskIndex), SizeGb: uint64(disk.SizeGB)})
	}
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
	binary.BigEndian.PutUint64(number[:], uint64(cpuCores))
	spec.Write(number[:])
	binary.BigEndian.PutUint64(number[:], uint64(memoryMB))
	spec.Write(number[:])
	binary.BigEndian.PutUint64(number[:], uint64(bootDiskGB))
	spec.Write(number[:])
	for _, disk := range input.AdditionalDisks {
		binary.BigEndian.PutUint64(number[:], uint64(disk.DiskIndex))
		spec.Write(number[:])
		binary.BigEndian.PutUint64(number[:], uint64(disk.SizeGB))
		spec.Write(number[:])
	}
	spec.Write([]byte(input.SSHPublicKey))
	specHash := spec.Sum(nil)

	now := time.Now().UTC()
	providerName := "aurora-" + vmID.String()
	vm := &hypervisorEntity.PersonalVM{
		ID:                    vmID,
		WorkspaceID:           input.WorkspaceID,
		ZoneID:                input.ZoneID,
		OwnerUserID:           input.OwnerUserID,
		Name:                  input.Name,
		Image:                 image.Name,
		ImageID:               &image.ID,
		ImageRevision:         &image.Revision,
		ImageSHA256:           image.SHA256,
		ResourceProfileCode:   input.ResourceProfileCode,
		CPUCores:              cpuCores,
		MemoryMB:              memoryMB,
		BootDiskGB:            bootDiskGB,
		DiskGB:                totalDiskGB,
		AdditionalDiskSizesGB: additionalDiskSizes,
		SSHPublicKey:          input.SSHPublicKey,
		SpecHash:              specHash,
		Status:                hypervisorEntity.VMStatusProvisioning,
		OperationID:           operationID,
		ProviderName:          providerName,
		CreatedAt:             now,
		UpdatedAt:             now,
	}

	payload, err := proto.Marshal(&hypervisorproto.VmCreateV1{
		SchemaVersion:        1,
		VmId:                 vmID[:],
		ProviderName:         providerName,
		ImageId:              image.ID[:],
		CpuCores:             uint32(cpuCores),
		MemoryMb:             uint64(memoryMB),
		DiskGb:               uint64(bootDiskGB),
		SshPublicKey:         input.SSHPublicKey,
		ConfigHash:           specHash,
		ImageRevision:        uint64(image.Revision),
		ImageSha256:          image.SHA256,
		ProviderTemplateVmid: uint64(*image.ProviderTemplateVMID),
		ResourceProfileCode:  input.ResourceProfileCode,
		AdditionalDisks:      protoDisks,
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
		OwnerID:              input.OwnerUserID,
		OwnerType:            "PERSONAL",
		Status:               "PENDING",
		JobVersion:           1,
		ResourceID:           vmID.String(),
		ResourceName:         input.Name,
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

func (s *PersonalVMServiceImpl) Delete(
	ctx context.Context,
	vmID uuid.UUID,
	workspaceID uuid.UUID,
	zoneID uuid.UUID,
	ownerUserID uuid.UUID,
) (out *hypervisorEntity.PersonalVMDeleteResult, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, hypervisorTaxonomy.ErrNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, hypervisorTaxonomy.ErrVMStateConflict):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		id := spanContext.TraceID()
		traceID = id[:]
	}
	return s.repo.BeginDelete(ctx, vmID, workspaceID, zoneID, ownerUserID, traceID)
}
