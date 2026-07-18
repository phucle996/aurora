package service

import (
	"context"
	"errors"
	"testing"

	"cost-manager/api/internal/domain/entity"
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

func TestActivatePersonalFreeTierNormalizesTrustedHeaders(t *testing.T) {
	ownerID := uuid.New()
	repo := &accountRepoStub{result: &entity.FreeTierAccount{OwnerID: ownerID}}
	service := NewAccountService(repo)

	_, err := service.ActivatePersonalFreeTier(context.Background(), "  "+ownerID.String()+"  ", " activation-1 ")
	if err != nil {
		t.Fatalf("ActivatePersonalFreeTier() error = %v", err)
	}
	if repo.command.OwnerID != ownerID || repo.command.OwnerType != entity.OwnerTypePersonal || repo.command.IdempotencyKey != "activation-1" {
		t.Fatalf("normalized command = %#v", repo.command)
	}
}

func TestActivatePersonalFreeTierRejectsInvalidIdentityAndIdempotency(t *testing.T) {
	service := NewAccountService(&accountRepoStub{})
	tests := []struct {
		name  string
		owner string
		key   string
	}{
		{name: "invalid owner", owner: "spoofed", key: "request-1"},
		{name: "missing key", owner: uuid.NewString(), key: "   "},
		{name: "oversized key", owner: uuid.NewString(), key: string(make([]byte, 129))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.ActivatePersonalFreeTier(context.Background(), test.owner, test.key)
			if !errors.Is(err, billingTaxonomy.ErrInvalidArgument) {
				t.Fatalf("error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}
