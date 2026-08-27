package mailRepoInterface

import (
	"context"

	mailEntity "controlplane/internal/mail/domain/entity"
)

type CommercialAdmissionProjectionRepository interface {
	Upsert(context.Context, *mailEntity.CommercialAdmissionProjection) error
}
