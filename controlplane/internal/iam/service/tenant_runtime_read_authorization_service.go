package iamSvcImpl

import (
	"context"
	"strings"

	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamproto "controlplane/internal/iam/transport/proto"

	"github.com/google/uuid"
)

type TenantRuntimeReadAuthorizationService struct {
	cacheEngine *cacheengine.CacheRegistry
}

func NewTenantRuntimeReadAuthorizationService(cacheEngine *cacheengine.CacheRegistry) iamSvcInterface.TenantRuntimeReadAuthorizationService {
	return &TenantRuntimeReadAuthorizationService{cacheEngine: cacheEngine}
}

func (s *TenantRuntimeReadAuthorizationService) Authorize(ctx context.Context, command iamEntity.TenantRuntimeReadAuthorization) (bool, error) {
	value, err := s.cacheEngine.GetOrLoad(ctx, "membership_role", command.ActorUserID.String()+":"+command.TenantID.String())
	if err != nil {
		return false, err
	}
	role, ok := value.(*iamproto.RoleEntry)
	if !ok || role == nil {
		return false, nil
	}

	expected := command.TenantID.String() + ":" + command.WorkspaceID.String() + ":" + command.Permission
	wildcard := command.TenantID.String() + ":*:" + command.Permission
	for _, raw := range role.Permissions {
		candidate := strings.ReplaceAll(raw, uuid.Nil.String(), "*")
		if strings.EqualFold(candidate, expected) || strings.EqualFold(candidate, wildcard) {
			return true, nil
		}
	}
	return false, nil
}
