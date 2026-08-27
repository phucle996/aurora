package iamRepoInterface

import (
	"context"

	"github.com/google/uuid"
)

type PersonalRuntimeReadAuthorizationRepository interface {
	ListPermissions(context.Context, uuid.UUID) ([]string, error)
}
