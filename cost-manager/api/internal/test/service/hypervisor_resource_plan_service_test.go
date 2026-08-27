package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	hypervisorresourceplanv1 "cost-manager/api/internal/genproto/billing/hypervisor/v1"
	"cost-manager/api/internal/service"
	billingTaxonomy "cost-manager/api/internal/taxonomy"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

type hypervisorResourcePlanRepoStub struct {
	billingRepoInterface.HypervisorResourcePlanRepository
	command entity.CreateHypervisorResourcePlanCommand
}

func (r *hypervisorResourcePlanRepoStub) CreateHypervisorResourcePlan(_ context.Context, command entity.CreateHypervisorResourcePlanCommand) (*entity.HypervisorResourcePlanRevision, error) {
	r.command = command
	return &entity.HypervisorResourcePlanRevision{PlanID: command.PlanID, RevisionID: command.RevisionID, RevisionNumber: 1, Code: command.Code, DisplayName: command.DisplayName, Description: command.Description, BillingModel: "LIMIT_HOURLY", CPUCores: command.CPUCores, MemoryMIB: command.MemoryMIB, BootDiskGIB: command.BootDiskGIB, ContentSHA256: command.ContentSHA256, EffectiveFrom: command.EffectiveFrom, State: "ACTIVE"}, nil
}

func TestHypervisorResourcePlanCreateWritesTypedOutboxPayload(t *testing.T) {
	repo := &hypervisorResourcePlanRepoStub{}
	created, err := service.NewHypervisorResourcePlanService(repo, nil, entity.HypervisorResourcePlanRelayPolicy{}).CreateHypervisorResourcePlan(context.Background(), entity.CreateHypervisorResourcePlanCommand{
		Code: "compute.standard", DisplayName: "Compute standard", Description: "Balanced VM", CPUCores: 2, MemoryMIB: 4096, BootDiskGIB: 64,
		EffectiveFrom: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC), ChangeReason: "initial commercial offer", CreatedBy: uuid.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.PlanID == uuid.Nil || created.RevisionID == uuid.Nil || len(repo.command.OutboxPayload) == 0 {
		t.Fatalf("plan creation did not produce an immutable outbox command: %#v", repo.command)
	}
	var event hypervisorresourceplanv1.EffectiveHypervisorResourcePlanV1
	if err := proto.Unmarshal(repo.command.OutboxPayload, &event); err != nil {
		t.Fatalf("outbox is not typed protobuf: %v", err)
	}
	if event.GetPlanId() == nil || event.GetRevisionId() == nil || event.GetCpuCores() != 2 || event.GetMemoryMib() != 4096 || event.GetBootDiskGib() != 64 || event.GetBillingModel() != "LIMIT_HOURLY" || len(event.GetContentSha256()) != 32 {
		t.Fatalf("unexpected resource plan event: %#v", event)
	}
}

func TestHypervisorResourcePlanCreateRejectsBusinessBoundsBeforeRepository(t *testing.T) {
	repo := &hypervisorResourcePlanRepoStub{}
	_, err := service.NewHypervisorResourcePlanService(repo, nil, entity.HypervisorResourcePlanRelayPolicy{}).CreateHypervisorResourcePlan(context.Background(), entity.CreateHypervisorResourcePlanCommand{
		Code: "too-large", DisplayName: "Too large", CPUCores: 1025, MemoryMIB: 4096, BootDiskGIB: 64,
		EffectiveFrom: time.Now().UTC(), ChangeReason: "test", CreatedBy: uuid.New(),
	})
	if !errors.Is(err, billingTaxonomy.ErrInvalidArgument) || repo.command.PlanID != uuid.Nil {
		t.Fatalf("invalid resource plan reached repository: command=%#v err=%v", repo.command, err)
	}
}

func TestResourcePlanBootDiskBoundary(t *testing.T) {
	for _, disk := range []int64{65536, 65537} {
		repo := &hypervisorResourcePlanRepoStub{}
		_, err := service.NewHypervisorResourcePlanService(repo, nil, entity.HypervisorResourcePlanRelayPolicy{}).CreateHypervisorResourcePlan(context.Background(), entity.CreateHypervisorResourcePlanCommand{
			Code: "disk-limit", DisplayName: "Disk limit", CPUCores: 2, MemoryMIB: 4096, BootDiskGIB: disk,
			EffectiveFrom: time.Now().UTC(), ChangeReason: "boundary", CreatedBy: uuid.New(),
		})
		if disk == 65536 && err != nil {
			t.Fatal(err)
		}
		if disk == 65537 && (!errors.Is(err, billingTaxonomy.ErrInvalidArgument) || repo.command.PlanID != uuid.Nil) {
			t.Fatalf("oversize accepted: %v", err)
		}
	}
}
