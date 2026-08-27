package unit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorSvcImpl "controlplane/internal/hypervisor/service"
	hypervisorTaxonomy "controlplane/internal/hypervisor/taxonomy"
	hypervisorProto "controlplane/internal/hypervisor/transport/proto"
	"controlplane/internal/observability"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

type personalVMResourcePlanRepoStub struct {
	imageRead bool
	imageErr  error
}

func (r *personalVMResourcePlanRepoStub) GetAvailableImage(_ context.Context, _, _ uuid.UUID) (*hypervisorEntity.ImageArtifact, error) {
	r.imageRead = true
	if r.imageErr != nil {
		return nil, r.imageErr
	}
	return nil, errors.New("image lookup must not run for an unavailable resource plan")
}

func TestPersonalVMServiceUsesOwnedL2BeforeRepository(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })
	planID := uuid.New()
	revisionID := uuid.New()
	payload, err := proto.Marshal(&hypervisorProto.EffectiveHypervisorResourcePlanV1{
		SchemaVersion: 1, PlanId: planID[:], RevisionId: revisionID[:], RevisionNumber: 1,
		BillingModel: "LIMIT_HOURLY", State: "ACTIVE", CpuCores: 2, MemoryMib: 4096, BootDiskGib: 64,
		ContentSha256: make([]byte, 32), EffectiveFrom: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), AllowedOperations: []string{"CREATE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(context.Background(), "controlplane:hypervisor:resource-plan:v1:"+planID.String()+":"+revisionID.String(), payload, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	imageErr := errors.New("image lookup reached after L2 admission")
	repo := &personalVMResourcePlanRepoStub{imageErr: imageErr}
	service := hypervisorSvcImpl.NewPersonalVMService(repo, redisClient, observability.NewNoopWorkflowRecorder())

	_, err = service.Create(context.Background(), &hypervisorEntity.CreatePersonalVM{
		WorkspaceID: uuid.New(), ZoneID: uuid.New(), OwnerUserID: uuid.New(), Name: "vm-one",
		ImageID: uuid.New(), ResourcePlanID: planID, ResourcePlanRevisionID: revisionID, SSHPublicKey: "ssh-ed25519 test",
	})
	if !errors.Is(err, imageErr) || !repo.imageRead {
		t.Fatalf("service did not use owned L2 before repository: image_read=%v err=%v", repo.imageRead, err)
	}
}

func (*personalVMResourcePlanRepoStub) CreateOrGet(context.Context, *hypervisorEntity.PersonalVM, *hypervisorEntity.HypervisorOutboxRecord) (*hypervisorEntity.PersonalVMCreateResult, error) {
	return nil, errors.New("create must not run for an ineffective resource plan")
}

func (*personalVMResourcePlanRepoStub) List(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int32) ([]*hypervisorEntity.PersonalVM, error) {
	return nil, nil
}

func (*personalVMResourcePlanRepoStub) Get(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (*hypervisorEntity.PersonalVM, error) {
	return nil, nil
}

func (*personalVMResourcePlanRepoStub) BeginDelete(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, []byte) (*hypervisorEntity.PersonalVMDeleteResult, error) {
	return nil, nil
}

func TestPersonalVMServiceRejectsResourcePlanCacheMissBeforeComposingVM(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })
	planID := uuid.New()
	revisionID := uuid.New()
	repo := &personalVMResourcePlanRepoStub{}
	service := hypervisorSvcImpl.NewPersonalVMService(repo, redisClient, observability.NewNoopWorkflowRecorder())

	_, err := service.Create(context.Background(), &hypervisorEntity.CreatePersonalVM{
		WorkspaceID: uuid.New(), ZoneID: uuid.New(), OwnerUserID: uuid.New(), Name: "vm-one",
		ImageID: uuid.New(), ResourcePlanID: planID, ResourcePlanRevisionID: revisionID, SSHPublicKey: "ssh-ed25519 test",
	})
	if !errors.Is(err, hypervisorTaxonomy.ErrResourcePlanCacheUnavailable) {
		t.Fatalf("expected resource plan admission failure, got %v", err)
	}
	if repo.imageRead {
		t.Fatal("service composed VM state after an L2 cache miss")
	}
}

func TestPersonalVMServiceEnforcesBootAndTotalDiskLimit(t *testing.T) {
	for _, test := range []struct {
		name    string
		boot    uint64
		extra   int64
		allowed bool
	}{
		{"boot-at-limit", 65536, 0, true},
		{"boot-over-limit-no-extra", 65537, 0, false},
		{"total-at-limit", 65528, 8, true},
		{"total-over-limit", 65536, 8, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr()})
			defer client.Close()
			planID, revisionID := uuid.New(), uuid.New()
			payload, err := proto.Marshal(&hypervisorProto.EffectiveHypervisorResourcePlanV1{
				SchemaVersion: 1, PlanId: planID[:], RevisionId: revisionID[:], RevisionNumber: 1,
				BillingModel: "LIMIT_HOURLY", State: "ACTIVE", CpuCores: 2, MemoryMib: 4096, BootDiskGib: test.boot,
				ContentSha256: make([]byte, 32), EffectiveFrom: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), AllowedOperations: []string{"CREATE"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := client.Set(context.Background(), "controlplane:hypervisor:resource-plan:v1:"+planID.String()+":"+revisionID.String(), payload, time.Minute).Err(); err != nil {
				t.Fatal(err)
			}
			repo := &personalVMResourcePlanRepoStub{imageErr: errors.New("image boundary reached")}
			svc := hypervisorSvcImpl.NewPersonalVMService(repo, client, observability.NewNoopWorkflowRecorder())
			input := &hypervisorEntity.CreatePersonalVM{ResourcePlanID: planID, ResourcePlanRevisionID: revisionID}
			if test.extra != 0 {
				input.AdditionalDisks = []hypervisorEntity.PersonalVMCreateAdditionalDisk{{SizeGB: test.extra, DiskIndex: 1}}
			}
			_, _ = svc.Create(context.Background(), input)
			if repo.imageRead != test.allowed {
				t.Fatalf("imageRead=%v allowed=%v", repo.imageRead, test.allowed)
			}
		})
	}
}
