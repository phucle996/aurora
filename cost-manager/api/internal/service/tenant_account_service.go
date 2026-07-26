package service

import (
	"context"

	billingRepoInterface "cost-manager/api/internal/domain/repo"

	"github.com/google/uuid"
)

type tenantAccountService struct {
	repo billingRepoInterface.TenantAccountRepository
}

func NewTenantAccountService(
	repo billingRepoInterface.TenantAccountRepository,
) *tenantAccountService {
	return &tenantAccountService{repo: repo}
}

func (s *tenantAccountService) ProvisionTenantWallet(
	ctx context.Context,
	eventID uuid.UUID,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	payloadHash string,
) error {
	return s.repo.ApplyTenantWalletProvision(ctx, eventID, tenantID, actorID, payloadHash)
}
