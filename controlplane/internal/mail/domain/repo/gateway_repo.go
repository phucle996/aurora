package mailRepoInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type GatewayRepository interface {
	Create(ctx context.Context, g *mailEntity.Gateway) error
	GetByID(ctx context.Context, tenantID, id string) (*mailEntity.Gateway, error)
	List(ctx context.Context, tenantID string) ([]*mailEntity.Gateway, error)
	Update(ctx context.Context, g *mailEntity.Gateway) error
	Delete(ctx context.Context, tenantID, id string) error
}
