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

type PersonalRuntimeReadAuthorizationService struct {
	cacheEngine *cacheengine.CacheRegistry
}

func NewPersonalRuntimeReadAuthorizationService(cacheEngine *cacheengine.CacheRegistry) iamSvcInterface.PersonalRuntimeReadAuthorizationService {
	return &PersonalRuntimeReadAuthorizationService{cacheEngine: cacheEngine}
}

func (s *PersonalRuntimeReadAuthorizationService) Authorize(ctx context.Context, command iamEntity.PersonalRuntimeReadAuthorization) (bool, error) {
	value, err := s.cacheEngine.GetOrLoad(ctx, "user_role", command.ActorUserID.String())
	if err != nil {
		return false, err
	}
	role, ok := value.(*iamproto.RoleEntry)
	if !ok || role == nil {
		return false, nil
	}

	expected := command.ActorUsername + ":" + command.WorkspaceID.String() + ":" + command.Permission
	wildcard := command.ActorUsername + ":*:" + command.Permission
	for _, raw := range role.Permissions {
		candidate := strings.ReplaceAll(raw, uuid.Nil.String(), "*")
		if strings.EqualFold(candidate, expected) || strings.EqualFold(candidate, wildcard) {
			return true, nil
		}
	}
	return false, nil
}
