package billingRepoInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
)

// AccountRepository owns wallet provisioning, onboarding and referral campaign
// state. Payment intent and provider settlement transactions live behind the
// dedicated payment repository contracts below.
type AccountRepository interface {
	ApplyPersonalWalletProvision(ctx context.Context, eventID uuid.UUID, ownerID uuid.UUID, payloadHash string) error
	ApplyTenantWalletProvision(ctx context.Context, eventID uuid.UUID, tenantID uuid.UUID, actorID uuid.UUID, payloadHash string) error
	GetPersonalWalletSummary(ctx context.Context, ownerID uuid.UUID) (*entity.WalletSummary, error)
	GetOnboarding(ctx context.Context, ownerID uuid.UUID, minimumTopUp int64) (*entity.OnboardingSnapshot, error)
	ReserveReferral(ctx context.Context, command entity.ReserveReferralCommand) (*entity.ReferralReservation, error)
	ListReferralCampaigns(ctx context.Context) ([]entity.ReferralCampaign, error)
	CreateReferralCampaign(ctx context.Context, command entity.CreateReferralCampaignCommand) (*entity.ReferralCampaign, error)
	UpdateReferralCampaignStatus(ctx context.Context, command entity.UpdateReferralCampaignStatusCommand) (*entity.ReferralCampaign, error)
}
