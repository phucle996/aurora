package mailRepoInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"

	"github.com/google/uuid"
)

type EndpointRepository interface {
	Create(ctx context.Context, e *mailEntity.Endpoint) error
	GetByID(ctx context.Context, zoneID uuid.UUID, id uuid.UUID) (*mailEntity.Endpoint, error)
	List(ctx context.Context, zoneID uuid.UUID) ([]*mailEntity.Endpoint, error)
	Update(ctx context.Context, e *mailEntity.Endpoint) error
	Delete(ctx context.Context, zoneID uuid.UUID, id uuid.UUID) error
}
