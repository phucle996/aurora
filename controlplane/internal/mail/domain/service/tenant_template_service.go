package mailSvcInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type TenantTemplateService interface {
	CreateTemplate(context.Context, *mailEntity.TenantTemplate) (*mailEntity.TenantTemplate, error)
	GetTemplate(context.Context, *mailEntity.TenantTemplate) (*mailEntity.TenantTemplate, error)
	ListTemplates(context.Context, *mailEntity.TenantTemplate) ([]*mailEntity.TenantTemplate, error)
	ListTemplateVersions(context.Context, *mailEntity.TenantTemplate) ([]*mailEntity.TenantTemplate, error)
	PublishTemplateVersion(context.Context, *mailEntity.TenantTemplate) (*mailEntity.TenantTemplate, error)
	ArchiveTemplate(context.Context, *mailEntity.TenantTemplate) error
}
