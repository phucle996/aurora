package mailRepoInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type PersonalConsumerRepository interface {
	Create(context.Context, *mailEntity.PersonalConsumer, *mailEntity.MailOutboxRecord) error
	GetByID(context.Context, *mailEntity.PersonalConsumer) (*mailEntity.PersonalConsumer, error)
	List(context.Context, *mailEntity.PersonalConsumer) ([]*mailEntity.PersonalConsumer, error)
	Update(context.Context, *mailEntity.PersonalConsumer, *mailEntity.MailOutboxRecord) error
	Delete(context.Context, *mailEntity.PersonalConsumer, *mailEntity.MailOutboxRecord) error
}
