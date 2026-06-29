package mailRepoInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type TemplateRepository interface {
	Create(ctx context.Context, t *mailEntity.Template) error
	GetByID(ctx context.Context, id string) (*mailEntity.Template, error)
	List(ctx context.Context) ([]*mailEntity.Template, error)
	Update(ctx context.Context, t *mailEntity.Template) error
	Delete(ctx context.Context, id string) error
}
