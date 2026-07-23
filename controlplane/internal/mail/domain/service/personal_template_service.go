package mailSvcInterface

import (
	"context"

	mailEntity "controlplane/internal/mail/domain/entity"
	"github.com/google/uuid"
)

type PersonalTemplateService interface {
	CreateTemplate(ctx context.Context, req *mailEntity.CreatePersonalTemplateRequest) (*mailEntity.CreatePersonalTemplateResponse, error)
	GetTemplate(ctx context.Context, req *mailEntity.GetPersonalTemplateRequest) (*mailEntity.GetPersonalTemplateResponse, error)
	ListTemplates(ctx context.Context, req *mailEntity.ListPersonalTemplatesRequest) ([]*mailEntity.PersonalTemplateItem, error)
	ListTemplateVersions(ctx context.Context, req *mailEntity.ListPersonalTemplateVersionsRequest) ([]*mailEntity.PersonalTemplateVersionItem, error)
	PublishTemplateVersion(ctx context.Context, req *mailEntity.PublishPersonalTemplateVersionRequest) (*mailEntity.PublishPersonalTemplateVersionResponse, error)
	DeleteTemplate(ctx context.Context, req *mailEntity.DeletePersonalTemplateRequest) (uuid.UUID, error)
}
