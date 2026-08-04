package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type RenderContextService interface {
	GetPersonalRenderContext(context.Context, *iamEntity.PersonalRenderContext) error
	GetTenantRenderContext(context.Context, *iamEntity.TenantRenderContext) error
}
