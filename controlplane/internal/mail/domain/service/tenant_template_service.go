package mailSvcInterface

import (
	"context"

	mailEntity "controlplane/internal/mail/domain/entity"
	"github.com/google/uuid"
)

type TenantTemplateService interface {
	CreateTemplate(ctx context.Context, req *mailEntity.CreateTenantTemplateRequest) (*mailEntity.CreateTenantTemplateResponse, error)
	GetTemplate(ctx context.Context, req *mailEntity.GetTenantTemplateRequest) (*mailEntity.GetTenantTemplateResponse, error)
	ListTemplates(ctx context.Context, req *mailEntity.ListTenantTemplatesRequest) ([]*mailEntity.TenantTemplateItem, error)
	ListTemplateVersions(ctx context.Context, req *mailEntity.ListTenantTemplateVersionsRequest) ([]*mailEntity.TenantTemplateVersionItem, error)
	PublishTemplateVersion(ctx context.Context, req *mailEntity.PublishTenantTemplateVersionRequest) (*mailEntity.PublishTenantTemplateVersionResponse, error)
	DeleteTemplate(ctx context.Context, req *mailEntity.DeleteTenantTemplateRequest) (uuid.UUID, error)
}
