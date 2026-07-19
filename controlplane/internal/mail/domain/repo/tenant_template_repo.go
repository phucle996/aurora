package mailRepoInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type TenantTemplateRepository interface {
	Create(context.Context, *mailEntity.TenantTemplate, *mailEntity.MailOutboxRecord) error
	GetByID(context.Context, *mailEntity.TenantTemplate) (*mailEntity.TenantTemplate, error)
	List(context.Context, *mailEntity.TenantTemplate) ([]*mailEntity.TenantTemplate, error)
	ListVersions(context.Context, *mailEntity.TenantTemplate) ([]*mailEntity.TenantTemplate, error)
	PublishVersion(context.Context, *mailEntity.TenantTemplate, *mailEntity.MailOutboxRecord) error
	Archive(context.Context, *mailEntity.TenantTemplate, *mailEntity.MailOutboxRecord) error
}
