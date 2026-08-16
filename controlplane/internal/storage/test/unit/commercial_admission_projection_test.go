package unit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	storageEntity "controlplane/internal/storage/domain/entity"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageSvcImpl "controlplane/internal/storage/service"
	storageTaxonomy "controlplane/internal/storage/taxonomy"
	admissionv1 "controlplane/internal/storage/transport/proto/admission"
	storageStream "controlplane/internal/storage/transport/stream"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

type commercialAdmissionProjectionRepoStub struct {
	projection *storageEntity.CommercialAdmissionProjection
}

func (r *commercialAdmissionProjectionRepoStub) Apply(
	_ context.Context,
	projection *storageEntity.CommercialAdmissionProjection,
) error {
	r.projection = projection
	return nil
}

func TestStorageCommercialAdmissionProjectionServiceAppliesOwnerDecisionOnly(t *testing.T) {
	repo := &commercialAdmissionProjectionRepoStub{}
	service := storageSvcImpl.NewStorageCommercialAdmissionProjectionService(repo)
	eventID := uuid.MustParse("31ed91e8-f03c-4431-986f-11140384d1a2")

	err := service.Apply(context.Background(), &storageEntity.CommercialAdmissionProjectionCommand{
		EventID:           eventID,
		OwnerID:           uuid.MustParse("85ea38ed-91d0-4684-8ce8-6367c7d709f1"),
		OwnerType:         "TENANT",
		PolicyVersion:     9,
		Decision:          "SUSPEND_BILLABLE",
		RestrictionReason: "INSUFFICIENT_BALANCE",
		EffectiveAt:       time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("apply projection: %v", err)
	}
	if repo.projection == nil || repo.projection.PolicyVersion != 9 {
		t.Fatalf("expected owner-scoped decision, got %#v", repo.projection)
	}
}

func TestStorageCommercialAdmissionProjectionServiceRejectsBusinessInvariant(t *testing.T) {
	repo := &commercialAdmissionProjectionRepoStub{}
	service := storageSvcImpl.NewStorageCommercialAdmissionProjectionService(repo)

	err := service.Apply(context.Background(), &storageEntity.CommercialAdmissionProjectionCommand{
		EventID:           uuid.MustParse("31ed91e8-f03c-4431-986f-11140384d1a2"),
		OwnerID:           uuid.MustParse("85ea38ed-91d0-4684-8ce8-6367c7d709f1"),
		OwnerType:         "PERSONAL",
		PolicyVersion:     1,
		Decision:          "ALLOW",
		RestrictionReason: "ALLOW_MUST_NOT_HAVE_A_REASON",
		EffectiveAt:       time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, storageTaxonomy.ErrInvalidCommercialAdmissionProjection) {
		t.Fatalf("expected invalid projection error, got %v", err)
	}
	if repo.projection != nil {
		t.Fatal("invalid projection reached the repository")
	}
}

type storageCommercialAdmissionTransportServiceStub struct {
	commands chan *storageEntity.CommercialAdmissionProjectionCommand
}

func (s *storageCommercialAdmissionTransportServiceStub) Apply(
	_ context.Context,
	command *storageEntity.CommercialAdmissionProjectionCommand,
) error {
	s.commands <- command
	return nil
}

var _ storageSvcInterface.CommercialAdmissionProjectionService = (*storageCommercialAdmissionTransportServiceStub)(nil)

func TestStorageCommercialAdmissionTransportProducesTypedCommand(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	service := &storageCommercialAdmissionTransportServiceStub{
		commands: make(chan *storageEntity.CommercialAdmissionProjectionCommand, 1),
	}
	consumer := storageStream.NewCommercialAdmissionProjectionConsumer(redisClient, service)
	if err := consumer.Start(); err != nil {
		t.Fatalf("start transport: %v", err)
	}
	t.Cleanup(func() { _ = redisClient.Close() })
	t.Cleanup(consumer.Stop)

	eventID := "31ed91e8-f03c-4431-986f-11140384d1a2"
	event := &admissionv1.CommercialAdmissionChangedV1{
		EventId:       eventID,
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
		Stream: "billing.commercial.admission.storage.changed.v1",
		Values: map[string]any{"event_id": eventID, "payload": payload},
	}).Err(); err != nil {
		t.Fatal(err)
	}

	select {
	case command := <-service.commands:
		if command.EventID != uuid.MustParse(eventID) || command.PolicyVersion != 7 {
			t.Fatalf("unexpected typed command: %#v", command)
		}
	case <-time.After(time.Second):
		t.Fatal("transport did not deliver typed command")
	}
}
