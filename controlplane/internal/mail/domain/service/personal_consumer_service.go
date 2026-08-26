package mailSvcInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type PersonalConsumerService interface {
	CreateConsumer(context.Context, *mailEntity.CreatePersonalConsumer) (*mailEntity.CreatePersonalConsumer, error)
	GetConsumer(context.Context, *mailEntity.GetPersonalConsumer) (*mailEntity.GetPersonalConsumer, error)
	ListConsumers(context.Context, *mailEntity.ListPersonalConsumer) ([]*mailEntity.ListPersonalConsumer, error)
	UpdateConsumer(context.Context, *mailEntity.UpdatePersonalConsumer) (*mailEntity.UpdatePersonalConsumer, error)
	ChangeConsumerState(context.Context, *mailEntity.ChangePersonalConsumerState) (*mailEntity.ChangePersonalConsumerState, error)
	DeleteConsumer(context.Context, *mailEntity.DeletePersonalConsumer) error
}
