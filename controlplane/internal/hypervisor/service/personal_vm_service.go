package hypervisorSvcImpl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"time"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
	hypervisorTaxonomy "controlplane/internal/hypervisor/taxonomy"
	hypervisorproto "controlplane/internal/hypervisor/transport/rpc/proto"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

type PersonalVMServiceImpl struct {
	repo hypervisorRepoInterface.PersonalVMRepository
}

func NewPersonalVMService(
	repo hypervisorRepoInterface.PersonalVMRepository,
) hypervisorSvcInterface.PersonalVMService {
	return &PersonalVMServiceImpl{repo: repo}
}

func (s *PersonalVMServiceImpl) Create(
	ctx context.Context,
	input *hypervisorEntity.CreatePersonalVM,
) (*hypervisorEntity.PersonalVMCreateResult, error) {
	vmID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	operationID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}

	spec := sha256.New()
	spec.Write([]byte(input.Image))
	spec.Write([]byte{0})
	var number [8]byte
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
		ID:           vmID,
		WorkspaceID:  input.WorkspaceID,
		ZoneID:       input.ZoneID,
		OwnerUserID:  input.OwnerUserID,
		Name:         input.Name,
		Image:        input.Image,
		CPUCores:     input.CPUCores,
		MemoryMB:     input.MemoryMB,
		DiskGB:       input.DiskGB,
		SSHPublicKey: input.SSHPublicKey,
		SpecHash:     specHash,
		Status:       hypervisorEntity.VMStatusProvisioning,
		OperationID:  operationID,
		ProviderName: providerName,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	payload, err := proto.Marshal(&hypervisorproto.VmCreateV1{
		SchemaVersion: 1,
		VmId:          vmID[:],
		ProviderName:  providerName,
		Image:         input.Image,
		CpuCores:      uint32(input.CPUCores),
		MemoryMb:      uint64(input.MemoryMB),
		DiskGb:        uint64(input.DiskGB),
		SshPublicKey:  input.SSHPublicKey,
		ConfigHash:    specHash,
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
		RoutingScope:         "zone:" + input.ZoneID.String(),
		JobTopic:             "hypervisor.vm.create",
		Payload:              payload,
		ActorUserID:          input.OwnerUserID,
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
	if !result.Created && result.VM.Status == hypervisorEntity.VMStatusFailed {
		return nil, hypervisorTaxonomy.ErrProvisioningFailed
	}
	return result, nil
}

func (s *PersonalVMServiceImpl) List(
	ctx context.Context,
	workspaceID uuid.UUID,
	zoneID uuid.UUID,
	ownerUserID uuid.UUID,
	limit int32,
) ([]*hypervisorEntity.PersonalVM, error) {
	return s.repo.List(ctx, workspaceID, zoneID, ownerUserID, limit)
}

func (s *PersonalVMServiceImpl) Get(
	ctx context.Context,
	vmID uuid.UUID,
	workspaceID uuid.UUID,
	ownerUserID uuid.UUID,
) (*hypervisorEntity.PersonalVM, error) {
	return s.repo.Get(ctx, vmID, workspaceID, ownerUserID)
}
