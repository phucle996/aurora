package iamSvcImpl

import (
	"context"
	"strings"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"

	"github.com/google/uuid"
)

type PersonalRuntimeReadAuthorizationService struct {
	repo iamRepoInterface.PersonalRuntimeReadAuthorizationRepository
}

func NewPersonalRuntimeReadAuthorizationService(repo iamRepoInterface.PersonalRuntimeReadAuthorizationRepository) iamSvcInterface.PersonalRuntimeReadAuthorizationService {
	return &PersonalRuntimeReadAuthorizationService{repo: repo}
}

func (s *PersonalRuntimeReadAuthorizationService) Authorize(ctx context.Context, command iamEntity.PersonalRuntimeReadAuthorization) (bool, error) {
	permissions, err := s.repo.ListPermissions(ctx, command.ActorUserID)
	if err != nil {
		return false, err
	}
	expected := command.ActorUsername + ":" + command.WorkspaceID.String() + ":" + command.Permission
	wildcard := command.ActorUsername + ":*:" + command.Permission
	for _, raw := range permissions {
		candidate := strings.ReplaceAll(raw, uuid.Nil.String(), "*")
		if strings.EqualFold(candidate, expected) || strings.EqualFold(candidate, wildcard) {
			return true, nil
		}
	}
	return false, nil
}
