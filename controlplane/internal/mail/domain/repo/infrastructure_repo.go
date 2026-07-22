package mailRepoInterface

import (
	"context"

	mailEntity "controlplane/internal/mail/domain/entity"

	"github.com/google/uuid"
)

// InfrastructureRepository chỉ expose read; reporter/JO là writer duy nhất của projection này.
type InfrastructureRepository interface {
	GetByZoneID(context.Context, uuid.UUID) (*mailEntity.MailInfrastructure, error)
}
