package iamRepoInterface

import (
	"context"

	"github.com/google/uuid"
)

type TenantRuntimeReadAuthorizationRepository interface {
	ListPermissions(context.Context, uuid.UUID, uuid.UUID) ([]string, error)
}
