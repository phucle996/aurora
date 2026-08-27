package iamRepoImpl

import (
	"context"

	"controlplane/internal/cacheengine"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamproto "controlplane/internal/iam/transport/proto"

	"github.com/google/uuid"
)

type PersonalRuntimeReadAuthorizationRepository struct {
	cacheEngine *cacheengine.CacheRegistry
}

func NewPersonalRuntimeReadAuthorizationRepository(cacheEngine *cacheengine.CacheRegistry) iamRepoInterface.PersonalRuntimeReadAuthorizationRepository {
	return &PersonalRuntimeReadAuthorizationRepository{cacheEngine: cacheEngine}
}

func (r *PersonalRuntimeReadAuthorizationRepository) ListPermissions(ctx context.Context, actorUserID uuid.UUID) ([]string, error) {
	value, err := r.cacheEngine.GetOrLoad(ctx, "user_role", actorUserID.String())
	if err != nil {
		return nil, err
	}
	role, ok := value.(*iamproto.RoleEntry)
	if !ok || role == nil {
		return nil, nil
	}
	return role.Permissions, nil
}
