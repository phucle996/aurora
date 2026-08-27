package iamSvcImpl

import (
	"context"
	"strings"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"

	"github.com/google/uuid"
)

type TenantRuntimeReadAuthorizationService struct {
	repo iamRepoInterface.TenantRuntimeReadAuthorizationRepository
}

func NewTenantRuntimeReadAuthorizationService(repo iamRepoInterface.TenantRuntimeReadAuthorizationRepository) iamSvcInterface.TenantRuntimeReadAuthorizationService {
	return &TenantRuntimeReadAuthorizationService{repo: repo}
}

func (s *TenantRuntimeReadAuthorizationService) Authorize(ctx context.Context, command iamEntity.TenantRuntimeReadAuthorization) (bool, error) {
	permissions, err := s.repo.ListPermissions(ctx, command.ActorUserID, command.TenantID)
	if err != nil {
		return false, err
	}
	expected := command.TenantID.String() + ":" + command.WorkspaceID.String() + ":" + command.Permission
	wildcard := command.TenantID.String() + ":*:" + command.Permission
	for _, raw := range permissions {
		candidate := strings.ReplaceAll(raw, uuid.Nil.String(), "*")
		if strings.EqualFold(candidate, expected) || strings.EqualFold(candidate, wildcard) {
			return true, nil
		}
	}
	return false, nil
}
