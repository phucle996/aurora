package mailSvcInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type TenantConsumerService interface {
	CreateConsumer(context.Context, *mailEntity.CreateTenantConsumer) (*mailEntity.CreateTenantConsumer, error)
	GetConsumer(context.Context, *mailEntity.GetTenantConsumer) (*mailEntity.GetTenantConsumer, error)
	ListConsumers(context.Context, *mailEntity.ListTenantConsumer) ([]*mailEntity.ListTenantConsumer, error)
	UpdateConsumer(context.Context, *mailEntity.UpdateTenantConsumer) (*mailEntity.UpdateTenantConsumer, error)
	ChangeConsumerState(context.Context, *mailEntity.ChangeTenantConsumerState) (*mailEntity.ChangeTenantConsumerState, error)
	DeleteConsumer(context.Context, *mailEntity.DeleteTenantConsumer) error
}
