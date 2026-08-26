package iamRepoImpl

import (
	"context"

	"controlplane/internal/cacheengine"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamproto "controlplane/internal/iam/transport/proto"

	"github.com/google/uuid"
)

type TenantRuntimeReadAuthorizationRepository struct {
	cacheEngine *cacheengine.CacheRegistry
}

func NewTenantRuntimeReadAuthorizationRepository(cacheEngine *cacheengine.CacheRegistry) iamRepoInterface.TenantRuntimeReadAuthorizationRepository {
	return &TenantRuntimeReadAuthorizationRepository{cacheEngine: cacheEngine}
}

func (r *TenantRuntimeReadAuthorizationRepository) ListPermissions(ctx context.Context, actorUserID uuid.UUID, tenantID uuid.UUID) ([]string, error) {
	value, err := r.cacheEngine.GetOrLoad(ctx, "membership_role", actorUserID.String()+":"+tenantID.String())
	if err != nil {
		return nil, err
	}
	role, ok := value.(*iamproto.RoleEntry)
	if !ok || role == nil {
		return nil, nil
	}
	return role.Permissions, nil
}
