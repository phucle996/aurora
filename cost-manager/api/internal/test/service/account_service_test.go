package service_test

import (
	"context"
	"errors"
	"testing"

	"cost-manager/api/internal/domain/entity"
	billingService "cost-manager/api/internal/service"
	billingTaxonomy "cost-manager/api/internal/taxonomy"

	"github.com/google/uuid"
)

type accountRepoStub struct {
	command entity.FreeTierActivation
	result  *entity.FreeTierAccount
	err     error
}

func (stub *accountRepoStub) ActivateFreeTier(_ context.Context, command entity.FreeTierActivation) (*entity.FreeTierAccount, error) {
	stub.command = command
	return stub.result, stub.err
}

func (stub *accountRepoStub) ApplyPersonalWalletProvision(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ string) error {
	return stub.err
}

func (stub *accountRepoStub) GetPersonalWalletSummary(_ context.Context, _ uuid.UUID) (*entity.WalletSummary, error) {
	return nil, stub.err
}

func TestActivatePersonalFreeTierNormalizesTrustedHeaders(t *testing.T) {
	ownerID := uuid.New()
	repo := &accountRepoStub{result: &entity.FreeTierAccount{OwnerID: ownerID}}
	service := billingService.NewAccountService(repo)

	if _, err := service.ActivatePersonalFreeTier(context.Background(), "  "+ownerID.String()+"  ", " activation-1 "); err != nil {
		t.Fatalf("ActivatePersonalFreeTier() error = %v", err)
	}
	if repo.command.OwnerID != ownerID || repo.command.OwnerType != entity.OwnerTypePersonal || repo.command.IdempotencyKey != "activation-1" {
		t.Fatalf("normalized command = %#v", repo.command)
	}
}

func TestGetPersonalWalletSummaryRejectsNilOwner(t *testing.T) {
	service := billingService.NewAccountService(&accountRepoStub{})
	if _, err := service.GetPersonalWalletSummary(context.Background(), uuid.Nil); !errors.Is(err, billingTaxonomy.ErrInvalidArgument) {
		t.Fatalf("error = %v, want ErrInvalidArgument", err)
	}
}

func TestActivatePersonalFreeTierRejectsInvalidIdentityAndIdempotency(t *testing.T) {
	service := billingService.NewAccountService(&accountRepoStub{})
	for _, test := range []struct {
		name  string
		owner string
		key   string
	}{
		{name: "invalid owner", owner: "spoofed", key: "request-1"},
		{name: "missing key", owner: uuid.NewString(), key: "   "},
		{name: "oversized key", owner: uuid.NewString(), key: string(make([]byte, 129))},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.ActivatePersonalFreeTier(context.Background(), test.owner, test.key); !errors.Is(err, billingTaxonomy.ErrInvalidArgument) {
				t.Fatalf("error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}
