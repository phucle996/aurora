package hypervisorRepoInterface

import (
	"context"

	"github.com/google/uuid"
)

type CommercialAdmissionRepository interface {
	RequirePersonalOwnerAdmission(context.Context, uuid.UUID) error
}
