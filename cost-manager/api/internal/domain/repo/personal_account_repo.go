package billingRepoInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
)

// PersonalAccountRepository owns personal wallet provisioning and onboarding.
// Referral reservations are part of this lifecycle because tenant accounts can
// never reserve or redeem onboarding credit.
type PersonalAccountRepository interface {
	ApplyPersonalWalletProvision(ctx context.Context, eventID uuid.UUID, ownerID uuid.UUID, payloadHash string) error
	GetOnboarding(ctx context.Context, ownerID uuid.UUID, minimumTopUp int64) (*entity.OnboardingSnapshot, error)
	ReserveReferral(ctx context.Context, command entity.ReserveReferralCommand) (*entity.ReferralReservation, error)
	ListReferralCampaigns(ctx context.Context) ([]entity.ReferralCampaign, error)
	CreateReferralCampaign(ctx context.Context, command entity.CreateReferralCampaignCommand) (*entity.ReferralCampaign, error)
	UpdateReferralCampaignStatus(ctx context.Context, command entity.UpdateReferralCampaignStatusCommand) (*entity.ReferralCampaign, error)
}
