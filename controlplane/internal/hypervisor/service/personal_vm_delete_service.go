package hypervisorSvcImpl

import (
	"context"
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

type PersonalVMDeleteServiceImpl struct {
	repo    hypervisorRepoInterface.PersonalVMDeleteRepository
	metrics observability.WorkflowRecorder
}

func NewPersonalVMDeleteService(repo hypervisorRepoInterface.PersonalVMDeleteRepository, metrics observability.WorkflowRecorder) hypervisorSvcInterface.PersonalVMDeleteService {
	return &PersonalVMDeleteServiceImpl{repo: repo, metrics: metrics}
}

func (s *PersonalVMDeleteServiceImpl) Delete(ctx context.Context, vmID, workspaceID, ownerUserID uuid.UUID) (out *hypervisorEntity.PersonalVMDeleteResult, err error) {
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

	target, err := s.repo.GetDeleteTarget(ctx, vmID, workspaceID, ownerUserID)
	if err != nil {
		return nil, err
	}
	if target.Status == hypervisorEntity.VMStatusDeleting {
		return &hypervisorEntity.PersonalVMDeleteResult{VMID: target.VMID, OperationID: target.OperationID, Status: target.Status}, nil
	}
	if target.Status != hypervisorEntity.VMStatusReady || target.ProviderVMID <= 0 {
		return nil, hypervisorTaxonomy.ErrVMStateConflict
	}
	operationID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	payload, err := proto.Marshal(&hypervisorproto.VmDeleteV1{
		SchemaVersion: 1,
		VmId:          target.VMID[:],
		ProviderName:  target.ProviderName,
		ProviderVmid:  uint64(target.ProviderVMID),
	})
	if err != nil {
		return nil, err
	}
	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		id := spanContext.TraceID()
		traceID = id[:]
	}
	return s.repo.BeginDelete(ctx, &hypervisorEntity.BeginPersonalVMDelete{
		Target: *target,
		Outbox: hypervisorEntity.HypervisorOutboxRecord{
			EventID: operationID, ZoneID: target.ZoneID, JobTopic: "hypervisor.vm.delete",
			Payload: payload, ActorUserID: &ownerUserID, OwnerID: target.OwnerUserID,
			OwnerType: "PERSONAL", Status: "PENDING", JobVersion: 1,
			ResourceID: target.VMID.String(), ResourceName: target.Name,
			PayloadSchemaVersion: 1, TraceID: traceID, IdleSeconds: 600,
		},
	})
}
