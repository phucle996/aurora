package unit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
	hypervisorSvcImpl "controlplane/internal/hypervisor/service"
	hypervisorTaxonomy "controlplane/internal/hypervisor/taxonomy"
	hypervisorProto "controlplane/internal/hypervisor/transport/proto"
	hypervisorStream "controlplane/internal/hypervisor/transport/stream"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

type commercialAdmissionProjectionRepoStub struct {
	projection *hypervisorEntity.CommercialAdmissionProjection
}

func (r *commercialAdmissionProjectionRepoStub) Upsert(
	_ context.Context,
	projection *hypervisorEntity.CommercialAdmissionProjection,
) error {
	r.projection = projection
	return nil
}

func TestHypervisorCommercialAdmissionProjectionServiceApply(t *testing.T) {
	repo := &commercialAdmissionProjectionRepoStub{}
	service := hypervisorSvcImpl.NewHypervisorCommercialAdmissionProjectionService(repo)
	eventID := uuid.MustParse("31ed91e8-f03c-4431-986f-11140384d1a2")

	err := service.Apply(context.Background(), &hypervisorEntity.CommercialAdmissionProjectionCommand{
		EventID:       eventID,
		OwnerID:       uuid.MustParse("85ea38ed-91d0-4684-8ce8-6367c7d709f1"),
		OwnerType:     "PERSONAL",
		PolicyVersion: 7,
		Decision:      "ALLOW",
		EffectiveAt:   time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("apply projection: %v", err)
	}
	if repo.projection == nil || repo.projection.EventID != eventID {
		t.Fatal("repository did not receive the typed projection")
	}
	if repo.projection.PolicyVersion != 7 || repo.projection.OwnerType != "PERSONAL" {
		t.Fatalf("unexpected typed projection: %#v", repo.projection)
	}
}

func TestHypervisorCommercialAdmissionProjectionServiceRejectsBusinessInvariant(t *testing.T) {
	repo := &commercialAdmissionProjectionRepoStub{}
	service := hypervisorSvcImpl.NewHypervisorCommercialAdmissionProjectionService(repo)

	err := service.Apply(context.Background(), &hypervisorEntity.CommercialAdmissionProjectionCommand{
		EventID:           uuid.MustParse("31ed91e8-f03c-4431-986f-11140384d1a2"),
		OwnerID:           uuid.MustParse("85ea38ed-91d0-4684-8ce8-6367c7d709f1"),
		OwnerType:         "PERSONAL",
		PolicyVersion:     7,
		Decision:          "ALLOW",
		RestrictionReason: "ALLOW_MUST_NOT_HAVE_A_REASON",
		EffectiveAt:       time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, hypervisorTaxonomy.ErrInvalidCommercialAdmissionProjection) {
		t.Fatalf("expected invalid projection error, got %v", err)
	}
	if repo.projection != nil {
		t.Fatal("invalid projection reached the repository")
	}
}

type hypervisorCommercialAdmissionTransportServiceStub struct {
	commands chan *hypervisorEntity.CommercialAdmissionProjectionCommand
}

func (s *hypervisorCommercialAdmissionTransportServiceStub) Apply(
	_ context.Context,
	command *hypervisorEntity.CommercialAdmissionProjectionCommand,
) error {
	s.commands <- command
	return nil
}

var _ hypervisorSvcInterface.CommercialAdmissionProjectionService = (*hypervisorCommercialAdmissionTransportServiceStub)(nil)

func TestHypervisorCommercialAdmissionTransportRejectsEnvelopeMismatch(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	service := &hypervisorCommercialAdmissionTransportServiceStub{
		commands: make(chan *hypervisorEntity.CommercialAdmissionProjectionCommand, 1),
	}
	consumer := hypervisorStream.NewCommercialAdmissionProjectionConsumer(redisClient, service)
	if err := consumer.Start(); err != nil {
		t.Fatalf("start transport: %v", err)
	}
	t.Cleanup(func() { _ = redisClient.Close() })
	t.Cleanup(consumer.Stop)

	event := &hypervisorProto.CommercialAdmissionChangedV1{
		EventId:       "31ed91e8-f03c-4431-986f-11140384d1a2",
		OwnerId:       "85ea38ed-91d0-4684-8ce8-6367c7d709f1",
		OwnerType:     "PERSONAL",
		PolicyVersion: 7,
		Decision:      "ALLOW",
		EffectiveAt:   "2026-08-16T10:00:00Z",
	}
	payload, err := proto.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.XAdd(context.Background(), &goredis.XAddArgs{
		Stream: "billing.commercial.admission.hypervisor.changed.v1",
		Values: map[string]any{
			"event_id": "381e8566-dadb-4f3f-a9c5-66c285beec33",
			"payload":  payload,
		},
	}).Err(); err != nil {
		t.Fatal(err)
	}

	settled := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		length, err := redisClient.XLen(context.Background(), "billing.commercial.admission.hypervisor.changed.v1").Result()
		if err != nil {
			t.Fatal(err)
		}
		if length == 0 {
			settled = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !settled {
		t.Fatal("transport-invalid event was not settled")
	}
	select {
	case command := <-service.commands:
		t.Fatalf("transport-invalid event reached service: %#v", command)
	default:
	}
}
