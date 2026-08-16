package unit_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	storageEntity "controlplane/internal/storage/domain/entity"
	storageSvcImpl "controlplane/internal/storage/service"

	"github.com/google/uuid"
)

type commercialAdmissionZoneRelayRepoStub struct {
	deliveries []storageEntity.CommercialAdmissionZoneDelivery
	released   []storageEntity.CommercialAdmissionZoneDelivery
	published  []storageEntity.CommercialAdmissionZoneDelivery
	lastError  string
}

func (r *commercialAdmissionZoneRelayRepoStub) Claim(
	context.Context,
	uuid.UUID,
	int,
) ([]storageEntity.CommercialAdmissionZoneDelivery, error) {
	return r.deliveries, nil
}

func (r *commercialAdmissionZoneRelayRepoStub) Release(
	_ context.Context,
	delivery storageEntity.CommercialAdmissionZoneDelivery,
	lastError string,
) error {
	r.released = append(r.released, delivery)
	r.lastError = lastError
	return nil
}

func (r *commercialAdmissionZoneRelayRepoStub) MarkPublished(
	_ context.Context,
	delivery storageEntity.CommercialAdmissionZoneDelivery,
) error {
	r.published = append(r.published, delivery)
	return nil
}

type commercialAdmissionZonePublisherStub struct {
	failResource uuid.UUID
}

func (p *commercialAdmissionZonePublisherStub) Publish(
	_ context.Context,
	delivery storageEntity.CommercialAdmissionZoneDelivery,
) error {
	if delivery.ResourceID == p.failResource {
		return errors.New(strings.Repeat("x", 2_048))
	}
	return nil
}

func TestCommercialAdmissionZoneRelaySettlesEachClaimIndependently(t *testing.T) {
	failedID := uuid.MustParse("b50ed940-0fe3-4b65-857c-1cc7469aa8f2")
	succeededID := uuid.MustParse("8132cca1-750f-4d5a-9df4-a0303cc81598")
	repo := &commercialAdmissionZoneRelayRepoStub{deliveries: []storageEntity.CommercialAdmissionZoneDelivery{
		{ResourceID: failedID},
		{ResourceID: succeededID},
	}}
	service := storageSvcImpl.NewStorageCommercialAdmissionZoneRelayService(
		repo,
		&commercialAdmissionZonePublisherStub{failResource: failedID},
	)

	published, err := service.RelayBatch(context.Background())
	if err != nil {
		t.Fatalf("relay batch: %v", err)
	}
	if published != 1 || len(repo.published) != 1 || repo.published[0].ResourceID != succeededID {
		t.Fatalf("unexpected published settlement: %#v", repo.published)
	}
	if len(repo.released) != 1 || repo.released[0].ResourceID != failedID {
		t.Fatalf("unexpected retry settlement: %#v", repo.released)
	}
	if len(repo.lastError) != 1_024 {
		t.Fatalf("persisted error is not bounded: %d", len(repo.lastError))
	}
}
