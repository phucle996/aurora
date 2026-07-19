package mailSvcInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type PersonalTemplateService interface {
	CreateTemplate(context.Context, *mailEntity.PersonalTemplate) (*mailEntity.PersonalTemplate, error)
	GetTemplate(context.Context, *mailEntity.PersonalTemplate) (*mailEntity.PersonalTemplate, error)
	ListTemplates(context.Context, *mailEntity.PersonalTemplate) ([]*mailEntity.PersonalTemplate, error)
	ListTemplateVersions(context.Context, *mailEntity.PersonalTemplate) ([]*mailEntity.PersonalTemplate, error)
	PublishTemplateVersion(context.Context, *mailEntity.PersonalTemplate) (*mailEntity.PersonalTemplate, error)
	DeleteTemplate(context.Context, *mailEntity.PersonalTemplate) error
}
