package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type PersonalRuntimeReadAuthorizationService interface {
	Authorize(context.Context, iamEntity.PersonalRuntimeReadAuthorization) (bool, error)
}
