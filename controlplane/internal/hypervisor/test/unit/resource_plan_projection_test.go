package unit_test

import (
	"context"
	"testing"
	"time"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
	hypervisorSvcImpl "controlplane/internal/hypervisor/service"
	hypervisorProto "controlplane/internal/hypervisor/transport/proto"
	hypervisorStream "controlplane/internal/hypervisor/transport/stream"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

type resourcePlanProjectionRepoStub struct {
	projection *hypervisorEntity.HypervisorResourcePlanProjection
	effective  []hypervisorEntity.HypervisorResourcePlanProjection
}

func (r *resourcePlanProjectionRepoStub) Insert(_ context.Context, projection *hypervisorEntity.HypervisorResourcePlanProjection) error {
	r.projection = projection
	return nil
}

func (r *resourcePlanProjectionRepoStub) ListEffective(context.Context) ([]hypervisorEntity.HypervisorResourcePlanProjection, error) {
	return r.effective, nil
}

func TestHypervisorResourcePlanProjectionRefreshesOwnedL2(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })
	planID := uuid.New()
	revisionID := uuid.New()
	repo := &resourcePlanProjectionRepoStub{effective: []hypervisorEntity.HypervisorResourcePlanProjection{{
		PlanID: planID, RevisionID: revisionID, RevisionNumber: 1, SourceEventID: uuid.New(),
		Code: "compute.standard", DisplayName: "Compute standard", BillingModel: "LIMIT_HOURLY",
		CPUCores: 2, MemoryMIB: 4096, BootDiskGIB: 64, ContentSHA256: make([]byte, 32), EffectiveFrom: time.Now().UTC().Add(-time.Minute), State: "ACTIVE", AllowCreate: true,
	}}}
	service := hypervisorSvcImpl.NewHypervisorResourcePlanProjectionService(repo, redisClient)
	if err := service.RefreshCache(context.Background()); err != nil {
		t.Fatalf("refresh owned L2: %v", err)
	}
	payload, err := redisClient.Get(context.Background(), "controlplane:hypervisor:resource-plan:v1:"+planID.String()+":"+revisionID.String()).Bytes()
	if err != nil {
		t.Fatalf("read owned L2: %v", err)
	}
	var cached hypervisorProto.EffectiveHypervisorResourcePlanV1
	if err := proto.Unmarshal(payload, &cached); err != nil || cached.RevisionNumber != 1 || string(cached.PlanId) != string(planID[:]) || string(cached.RevisionId) != string(revisionID[:]) {
		t.Fatalf("unexpected cached resource plan: cached=%#v err=%v", &cached, err)
	}
}

func TestHypervisorResourcePlanProjectionAppliesFlatRevision(t *testing.T) {
	repo := &resourcePlanProjectionRepoStub{}
	err := hypervisorSvcImpl.NewHypervisorResourcePlanProjectionService(repo, nil).Apply(context.Background(), &hypervisorEntity.HypervisorResourcePlanProjectionCommand{
		EventID: uuid.New(), PlanID: uuid.New(), RevisionID: uuid.New(), RevisionNumber: 1, Code: "compute.standard", DisplayName: "Compute standard", BillingModel: "LIMIT_HOURLY", CPUCores: 2, MemoryMIB: 4096, BootDiskGIB: 64, ContentSHA256: make([]byte, 32), EffectiveFrom: time.Now().UTC(), State: "ACTIVE", AllowedCreate: true,
	})
	if err != nil || repo.projection == nil || repo.projection.RevisionNumber != 1 || repo.projection.State != "ACTIVE" || !repo.projection.AllowCreate {
		t.Fatalf("typed resource plan projection was not written: projection=%#v err=%v", repo.projection, err)
	}
}

type hypervisorResourcePlanTransportServiceStub struct {
	commands chan *hypervisorEntity.HypervisorResourcePlanProjectionCommand
}

func (s *hypervisorResourcePlanTransportServiceStub) Apply(
	_ context.Context,
	command *hypervisorEntity.HypervisorResourcePlanProjectionCommand,
) error {
	s.commands <- command
	return nil
}

func (s *hypervisorResourcePlanTransportServiceStub) RefreshCache(_ context.Context) error {
	return nil
}

var _ hypervisorSvcInterface.HypervisorResourcePlanProjectionService = (*hypervisorResourcePlanTransportServiceStub)(nil)

func TestHypervisorResourcePlanTransportProjectsNonCreateRevision(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	service := &hypervisorResourcePlanTransportServiceStub{
		commands: make(chan *hypervisorEntity.HypervisorResourcePlanProjectionCommand, 1),
	}
	consumer := hypervisorStream.NewResourcePlanProjectionConsumer(redisClient, service)
	if err := consumer.Start(); err != nil {
		t.Fatalf("start transport: %v", err)
	}
	t.Cleanup(func() { _ = redisClient.Close() })
	t.Cleanup(consumer.Stop)

	eventID := uuid.New().String()
	planID := uuid.New()
	revisionID := uuid.New()
	event := &hypervisorProto.EffectiveHypervisorResourcePlanV1{
		SchemaVersion:     1,
		EventId:           eventID,
		PlanId:            planID[:],
		RevisionId:        revisionID[:],
		RevisionNumber:    1,
		Code:              "compute.standard",
		DisplayName:       "Compute Standard",
		BillingModel:      "LIMIT_HOURLY",
		CpuCores:          2,
		MemoryMib:         4096,
		BootDiskGib:       64,
		ContentSha256:     make([]byte, 32),
		EffectiveFrom:     time.Now().UTC().Format(time.RFC3339Nano),
		State:             "RETIRED",
		AllowedOperations: []string{"READ_ONLY"}, // Không có "CREATE"
	}
	payload, err := proto.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.XAdd(context.Background(), &goredis.XAddArgs{
		Stream: "billing.hypervisor.resource-plan.changed.v1",
		Values: map[string]any{"event_id": eventID, "payload": payload},
	}).Err(); err != nil {
		t.Fatal(err)
	}

	select {
	case command := <-service.commands:
		if command.State != "RETIRED" || command.AllowedCreate {
			t.Fatalf("non-create policy was not forwarded: %#v", command)
		}
	case <-time.After(time.Second):
		t.Fatal("non-create event did not reach the durable projection service")
	}
}

func TestHypervisorResourcePlanTransportProducesTypedCommand(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	service := &hypervisorResourcePlanTransportServiceStub{
		commands: make(chan *hypervisorEntity.HypervisorResourcePlanProjectionCommand, 1),
	}
	consumer := hypervisorStream.NewResourcePlanProjectionConsumer(redisClient, service)
	if err := consumer.Start(); err != nil {
		t.Fatalf("start transport: %v", err)
	}
	t.Cleanup(func() { _ = redisClient.Close() })
	t.Cleanup(consumer.Stop)

	eventID := uuid.New().String()
	planID := uuid.New()
	revisionID := uuid.New()
	event := &hypervisorProto.EffectiveHypervisorResourcePlanV1{
		SchemaVersion:     1,
		EventId:           eventID,
		PlanId:            planID[:],
		RevisionId:        revisionID[:],
		RevisionNumber:    1,
		Code:              "compute.standard",
		DisplayName:       "Compute Standard",
		BillingModel:      "LIMIT_HOURLY",
		CpuCores:          2,
		MemoryMib:         4096,
		BootDiskGib:       64,
		ContentSha256:     make([]byte, 32),
		EffectiveFrom:     time.Now().UTC().Format(time.RFC3339Nano),
		State:             "ACTIVE",
		AllowedOperations: []string{"CREATE"},
	}
	payload, err := proto.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.XAdd(context.Background(), &goredis.XAddArgs{
		Stream: "billing.hypervisor.resource-plan.changed.v1",
		Values: map[string]any{"event_id": eventID, "payload": payload},
	}).Err(); err != nil {
		t.Fatal(err)
	}

	select {
	case command := <-service.commands:
		if command.EventID != uuid.MustParse(eventID) || command.PlanID != planID || command.RevisionID != revisionID || command.Code != "compute.standard" {
			t.Fatalf("unexpected command forwarded to service: %#v", command)
		}
	case <-time.After(time.Second):
		t.Fatal("transport did not deliver command to service")
	}
}
