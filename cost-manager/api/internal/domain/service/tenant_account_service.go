package billingSvcInterface

import (
	"context"

	"github.com/google/uuid"
)

type TenantAccountService interface {
	ProvisionTenantWallet(
		ctx context.Context,
		eventID uuid.UUID,
		tenantID uuid.UUID,
		actorID uuid.UUID,
		payloadHash string,
	) error
}
