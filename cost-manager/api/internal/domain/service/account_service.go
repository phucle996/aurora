package billingSvcInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
)

type AccountService interface {
	ProvisionPersonalWallet(ctx context.Context, eventID uuid.UUID, ownerID uuid.UUID, payloadHash string) error
	ProvisionTenantWallet(ctx context.Context, eventID uuid.UUID, tenantID uuid.UUID, actorID uuid.UUID, payloadHash string) error
	GetOnboarding(ctx context.Context, ownerID uuid.UUID) (*entity.OnboardingSnapshot, error)
	ReserveReferral(ctx context.Context, command entity.ReserveReferralCommand) (*entity.ReferralReservation, error)
	ListReferralCampaigns(ctx context.Context) ([]entity.ReferralCampaign, error)
	CreateReferralCampaign(ctx context.Context, command entity.CreateReferralCampaignCommand) (*entity.ReferralCampaign, error)
	UpdateReferralCampaignStatus(ctx context.Context, command entity.UpdateReferralCampaignStatusCommand) (*entity.ReferralCampaign, error)
}
