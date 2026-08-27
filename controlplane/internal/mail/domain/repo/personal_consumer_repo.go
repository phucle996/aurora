package mailRepoInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type PersonalConsumerRepository interface {
	Create(context.Context, *mailEntity.CreatePersonalConsumer, *mailEntity.MailOutboxRecord) error
	GetByID(context.Context, *mailEntity.GetPersonalConsumer) (*mailEntity.GetPersonalConsumer, error)
	List(context.Context, *mailEntity.ListPersonalConsumer) ([]*mailEntity.ListPersonalConsumer, error)
	Update(context.Context, *mailEntity.UpdatePersonalConsumer, *mailEntity.MailOutboxRecord) error
	Delete(context.Context, *mailEntity.DeletePersonalConsumer, *mailEntity.MailOutboxRecord) error
	LoadDrainTarget(context.Context, mailEntity.PersonalConsumerDrainCommand) (mailEntity.PersonalConsumerDrainTarget, error)
	RequestDrain(context.Context, mailEntity.PersonalConsumerDrainCommand, uint32, mailEntity.MailOutboxRecord) error
}
