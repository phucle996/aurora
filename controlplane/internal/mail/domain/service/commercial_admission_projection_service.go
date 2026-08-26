package mailSvcInterface

import (
	"context"

	mailEntity "controlplane/internal/mail/domain/entity"
)

type CommercialAdmissionProjectionService interface {
	Apply(context.Context, *mailEntity.CommercialAdmissionProjectionCommand) error
}
