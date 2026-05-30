package mailRepoInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type ConsumerRepository interface {
	Create(ctx context.Context, c *mailEntity.Consumer) error
	GetByID(ctx context.Context, tenantID, id string) (*mailEntity.Consumer, error)
	List(ctx context.Context, tenantID string, filterSource string, filterStatus string) ([]*mailEntity.Consumer, error)
	Update(ctx context.Context, c *mailEntity.Consumer) error
	Delete(ctx context.Context, tenantID, id string) error
}
