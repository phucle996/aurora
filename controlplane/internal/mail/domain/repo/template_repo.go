package mailRepoInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type TemplateRepository interface {
	Create(ctx context.Context, t *mailEntity.Template) error
	GetByID(ctx context.Context, tenantID, id string) (*mailEntity.Template, error)
	List(ctx context.Context, tenantID string) ([]*mailEntity.Template, error)
	Update(ctx context.Context, t *mailEntity.Template) error
	Delete(ctx context.Context, tenantID, id string) error
}
