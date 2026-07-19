package mailSvcInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type PersonalConsumerService interface {
	CreateConsumer(context.Context, *mailEntity.PersonalConsumer) (*mailEntity.PersonalConsumer, error)
	GetConsumer(context.Context, *mailEntity.PersonalConsumer) (*mailEntity.PersonalConsumer, error)
	ListConsumers(context.Context, *mailEntity.PersonalConsumer) ([]*mailEntity.PersonalConsumer, error)
	UpdateConsumer(context.Context, *mailEntity.PersonalConsumer) (*mailEntity.PersonalConsumer, error)
	ChangeConsumerState(context.Context, *mailEntity.PersonalConsumer) (*mailEntity.PersonalConsumer, error)
	DeleteConsumer(context.Context, *mailEntity.PersonalConsumer) error
}
