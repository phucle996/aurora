package mailSvcInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type TemplateService interface {
	CreateTemplate(ctx context.Context, params mailEntity.CreateTemplateParams) (*mailEntity.Template, error)
	GetTemplate(ctx context.Context, id string) (*mailEntity.Template, error)
	ListTemplates(ctx context.Context) ([]*mailEntity.Template, error)
	UpdateTemplate(ctx context.Context, params mailEntity.UpdateTemplateParams) (*mailEntity.Template, error)
	DeleteTemplate(ctx context.Context, id string) error
}
