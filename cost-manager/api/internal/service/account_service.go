package service

import (
	"context"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"

	"github.com/google/uuid"
)

type accountService struct {
	repo   billingRepoInterface.AccountRepository
	policy entity.PaymentPolicy
}

func NewAccountService(
	repo billingRepoInterface.AccountRepository,
	policy entity.PaymentPolicy,
) *accountService {
	return &accountService{
		repo:   repo,
		policy: policy,
	}
}

func (s *accountService) ProvisionPersonalWallet(
	ctx context.Context,
	eventID uuid.UUID,
	ownerID uuid.UUID,
	payloadHash string,
) error {
	return s.repo.ApplyPersonalWalletProvision(ctx, eventID, ownerID, payloadHash)
}

func (s *accountService) ProvisionTenantWallet(
	ctx context.Context,
	eventID uuid.UUID,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	payloadHash string,
) error {
	return s.repo.ApplyTenantWalletProvision(ctx, eventID, tenantID, actorID, payloadHash)
}

func (s *accountService) GetOnboarding(
	ctx context.Context,
	ownerID uuid.UUID,
) (*entity.OnboardingSnapshot, error) {
	return s.repo.GetOnboarding(ctx, ownerID, s.policy.MinimumTopUp)
}

func (s *accountService) ReserveReferral(
	ctx context.Context,
	command entity.ReserveReferralCommand,
) (*entity.ReferralReservation, error) {
	command.ExpiresAt = time.Now().UTC().Add(s.policy.ReferralTTL)
	return s.repo.ReserveReferral(ctx, command)
}

func (s *accountService) ListReferralCampaigns(
	ctx context.Context,
) ([]entity.ReferralCampaign, error) {
	return s.repo.ListReferralCampaigns(ctx)
}

func (s *accountService) CreateReferralCampaign(
	ctx context.Context,
	command entity.CreateReferralCampaignCommand,
) (*entity.ReferralCampaign, error) {
	return s.repo.CreateReferralCampaign(ctx, command)
}

func (s *accountService) UpdateReferralCampaignStatus(
	ctx context.Context,
	command entity.UpdateReferralCampaignStatusCommand,
) (*entity.ReferralCampaign, error) {
	return s.repo.UpdateReferralCampaignStatus(ctx, command)
}
