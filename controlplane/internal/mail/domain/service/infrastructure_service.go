package mailSvcInterface

import (
	"context"

	mailEntity "controlplane/internal/mail/domain/entity"

	"github.com/google/uuid"
)

type InfrastructureService interface {
	GetByZoneID(context.Context, uuid.UUID) (*mailEntity.MailInfrastructure, error)
}
