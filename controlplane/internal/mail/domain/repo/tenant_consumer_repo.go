package mailRepoInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type TenantConsumerRepository interface {
	Create(context.Context, *mailEntity.CreateTenantConsumer, *mailEntity.MailOutboxRecord) error
	GetByID(context.Context, *mailEntity.GetTenantConsumer) (*mailEntity.GetTenantConsumer, error)
	List(context.Context, *mailEntity.ListTenantConsumer) ([]*mailEntity.ListTenantConsumer, error)
	Update(context.Context, *mailEntity.UpdateTenantConsumer, *mailEntity.MailOutboxRecord) error
	Delete(context.Context, *mailEntity.DeleteTenantConsumer, *mailEntity.MailOutboxRecord) error
}
