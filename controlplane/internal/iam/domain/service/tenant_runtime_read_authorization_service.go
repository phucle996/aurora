package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type TenantRuntimeReadAuthorizationService interface {
	Authorize(context.Context, iamEntity.TenantRuntimeReadAuthorization) (bool, error)
}
