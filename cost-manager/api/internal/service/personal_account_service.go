package service

import (
	"context"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"

	"github.com/google/uuid"
)

type personalAccountService struct {
	repo   billingRepoInterface.PersonalAccountRepository
	policy entity.PaymentPolicy
}

func NewPersonalAccountService(
	repo billingRepoInterface.PersonalAccountRepository,
	policy entity.PaymentPolicy,
) *personalAccountService {
	return &personalAccountService{
		repo:   repo,
		policy: policy,
	}
}

func (s *personalAccountService) ProvisionPersonalWallet(
	ctx context.Context,
	eventID uuid.UUID,
	ownerID uuid.UUID,
	payloadHash string,
) error {
	return s.repo.ApplyPersonalWalletProvision(ctx, eventID, ownerID, payloadHash)
}

func (s *personalAccountService) GetOnboarding(
	ctx context.Context,
	ownerID uuid.UUID,
) (*entity.OnboardingSnapshot, error) {
	return s.repo.GetOnboarding(ctx, ownerID, s.policy.MinimumTopUp)
}

func (s *personalAccountService) ReserveReferral(
	ctx context.Context,
	command entity.ReserveReferralCommand,
) (*entity.ReferralReservation, error) {
	command.ExpiresAt = time.Now().UTC().Add(s.policy.ReferralTTL)
	return s.repo.ReserveReferral(ctx, command)
}

func (s *personalAccountService) ListReferralCampaigns(
	ctx context.Context,
) ([]entity.ReferralCampaign, error) {
	return s.repo.ListReferralCampaigns(ctx)
}

func (s *personalAccountService) CreateReferralCampaign(
	ctx context.Context,
	command entity.CreateReferralCampaignCommand,
) (*entity.ReferralCampaign, error) {
	return s.repo.CreateReferralCampaign(ctx, command)
}

func (s *personalAccountService) UpdateReferralCampaignStatus(
	ctx context.Context,
	command entity.UpdateReferralCampaignStatusCommand,
) (*entity.ReferralCampaign, error) {
	return s.repo.UpdateReferralCampaignStatus(ctx, command)
}
