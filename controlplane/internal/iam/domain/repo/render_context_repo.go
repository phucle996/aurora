package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type RenderContextRepository interface {
	GetPersonalRenderContext(context.Context, *iamEntity.PersonalRenderContext) error
	GetTenantRenderContext(context.Context, *iamEntity.TenantRenderContext) error
}
