package mailSvcInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type TenantConsumerService interface {
	CreateConsumer(context.Context, *mailEntity.TenantConsumer) (*mailEntity.TenantConsumer, error)
	GetConsumer(context.Context, *mailEntity.TenantConsumer) (*mailEntity.TenantConsumer, error)
	ListConsumers(context.Context, *mailEntity.TenantConsumer) ([]*mailEntity.TenantConsumer, error)
	UpdateConsumer(context.Context, *mailEntity.TenantConsumer) (*mailEntity.TenantConsumer, error)
	ChangeConsumerState(context.Context, *mailEntity.TenantConsumer) (*mailEntity.TenantConsumer, error)
	DeleteConsumer(context.Context, *mailEntity.TenantConsumer) error
}
