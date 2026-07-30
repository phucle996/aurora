package hierarchySvcImpl

import (
	"context"
	"fmt"
	"time"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchyRepoInterface "controlplane/internal/hierarchy/domain/repo"
	hierarchySvcInterface "controlplane/internal/hierarchy/domain/service"
	hierarchyMetrics "controlplane/internal/hierarchy/metrics"

	"github.com/google/uuid"
)

type PersonalWorkspaceServiceImpl struct {
	repo hierarchyRepoInterface.PersonalWorkspaceRepository
}

func NewPersonalWorkspaceService(repo hierarchyRepoInterface.PersonalWorkspaceRepository) hierarchySvcInterface.PersonalWorkspaceService {
	return &PersonalWorkspaceServiceImpl{repo: repo}
}

func (s *PersonalWorkspaceServiceImpl) CreateWorkspaceForPersonal(ctx context.Context, in *hierarchyEntity.CreatePersonalWorkspace) (*hierarchyEntity.CreatePersonalWorkspace, error) {
	workspaceID, err := uuid.NewV7()
	if err != nil {
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return nil, fmt.Errorf("generate personal workspace id: %w", err)
	}
	now := time.Now().UTC()
	in.ID = workspaceID
	in.CreatedAt = now
	in.UpdatedAt = now

	startedAt := time.Now()
	out, err := s.repo.CreateWorkspaceForPersonal(ctx, in)
	if err != nil {
		hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "CreateWorkspaceForPersonal", hierarchyMetrics.OutcomeFailure, time.Since(startedAt), err)
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return nil, err
	}
	hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "CreateWorkspaceForPersonal", hierarchyMetrics.OutcomeSuccess, time.Since(startedAt), nil)
	hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeSuccess)
	return out, nil
}

func (s *PersonalWorkspaceServiceImpl) ListWorkspacesForPersonal(ctx context.Context, in *hierarchyEntity.ListPersonalWorkspaces) ([]hierarchyEntity.ListPersonalWorkspaces, error) {
	startedAt := time.Now()
	items, err := s.repo.ListWorkspacesForPersonal(ctx, in)
	if err != nil {
		hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "ListWorkspacesForPersonal", hierarchyMetrics.OutcomeFailure, time.Since(startedAt), err)
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return nil, err
	}
	hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "ListWorkspacesForPersonal", hierarchyMetrics.OutcomeSuccess, time.Since(startedAt), nil)
	hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeSuccess)
	return items, nil
}

func (s *PersonalWorkspaceServiceImpl) ListWorkspaceCatalogForPersonal(ctx context.Context, in *hierarchyEntity.ListPersonalWorkspaceCatalog) ([]hierarchyEntity.ListPersonalWorkspaceCatalog, error) {
	startedAt := time.Now()
	items, err := s.repo.ListWorkspaceCatalogForPersonal(ctx, in)
	if err != nil {
		hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "ListWorkspaceCatalogForPersonal", hierarchyMetrics.OutcomeFailure, time.Since(startedAt), err)
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return nil, err
	}
	hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "ListWorkspaceCatalogForPersonal", hierarchyMetrics.OutcomeSuccess, time.Since(startedAt), nil)
	hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeSuccess)
	return items, nil
}

func (s *PersonalWorkspaceServiceImpl) DeleteWorkspaceForPersonal(ctx context.Context, in *hierarchyEntity.DeletePersonalWorkspace) error {
	startedAt := time.Now()
	err := s.repo.DeleteWorkspaceForPersonal(ctx, in)
	if err != nil {
		hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "DeleteWorkspaceForPersonal", hierarchyMetrics.OutcomeFailure, time.Since(startedAt), err)
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return err
	}
	hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "DeleteWorkspaceForPersonal", hierarchyMetrics.OutcomeSuccess, time.Since(startedAt), nil)
	hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeSuccess)
	return nil
}
