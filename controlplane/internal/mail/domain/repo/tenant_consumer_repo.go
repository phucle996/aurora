package mailRepoInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type TenantConsumerRepository interface {
	Create(context.Context, *mailEntity.TenantConsumer, *mailEntity.MailOutboxRecord) error
	GetByID(context.Context, *mailEntity.TenantConsumer) (*mailEntity.TenantConsumer, error)
	List(context.Context, *mailEntity.TenantConsumer) ([]*mailEntity.TenantConsumer, error)
	Update(context.Context, *mailEntity.TenantConsumer, *mailEntity.MailOutboxRecord) error
	Delete(context.Context, *mailEntity.TenantConsumer, *mailEntity.MailOutboxRecord) error
}
