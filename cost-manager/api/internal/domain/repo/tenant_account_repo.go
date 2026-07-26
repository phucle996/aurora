package billingRepoInterface

import (
	"context"

	"github.com/google/uuid"
)

// TenantAccountRepository has no referral surface. actorID is part of the
// durable replay fence because the tenant owner and the authorized human actor
// are different identities.
type TenantAccountRepository interface {
	ApplyTenantWalletProvision(
		ctx context.Context,
		eventID uuid.UUID,
		tenantID uuid.UUID,
		actorID uuid.UUID,
		payloadHash string,
	) error
}
