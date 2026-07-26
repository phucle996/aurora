package service_test

import (
	"context"
	"testing"

	billingService "cost-manager/api/internal/service"

	"github.com/google/uuid"
)

type tenantAccountRepoStub struct {
	eventID     uuid.UUID
	tenantID    uuid.UUID
	actorID     uuid.UUID
	payloadHash string
	err         error
}

func (s *tenantAccountRepoStub) ApplyTenantWalletProvision(
	_ context.Context,
	eventID uuid.UUID,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	payloadHash string,
) error {
	s.eventID = eventID
	s.tenantID = tenantID
	s.actorID = actorID
	s.payloadHash = payloadHash
	return s.err
}

func TestTenantAccountServicePreservesProvisionReplayFence(t *testing.T) {
	repo := &tenantAccountRepoStub{}
	service := billingService.NewTenantAccountService(repo)
	eventID := uuid.New()
	tenantID := uuid.New()
	actorID := uuid.New()
	const payloadHash = "tenant-provision-payload-hash"

	if err := service.ProvisionTenantWallet(
		context.Background(),
		eventID,
		tenantID,
		actorID,
		payloadHash,
	); err != nil {
		t.Fatalf("ProvisionTenantWallet() error = %v", err)
	}
	if repo.eventID != eventID ||
		repo.tenantID != tenantID ||
		repo.actorID != actorID ||
		repo.payloadHash != payloadHash {
		t.Fatalf("tenant replay fence changed before repository: %#v", repo)
	}
}
