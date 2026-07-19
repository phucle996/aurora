package mailRepoInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type PersonalTemplateRepository interface {
	Create(context.Context, *mailEntity.PersonalTemplate, *mailEntity.MailOutboxRecord) error
	GetByID(context.Context, *mailEntity.PersonalTemplate) (*mailEntity.PersonalTemplate, error)
	List(context.Context, *mailEntity.PersonalTemplate) ([]*mailEntity.PersonalTemplate, error)
	ListVersions(context.Context, *mailEntity.PersonalTemplate) ([]*mailEntity.PersonalTemplate, error)
	PublishVersion(context.Context, *mailEntity.PersonalTemplate, *mailEntity.MailOutboxRecord) error
	Archive(context.Context, *mailEntity.PersonalTemplate, *mailEntity.MailOutboxRecord) error
}
