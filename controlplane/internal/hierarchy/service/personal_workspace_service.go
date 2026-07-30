package hierarchySvcImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchyRepoInterface "controlplane/internal/hierarchy/domain/repo"
	hierarchySvcInterface "controlplane/internal/hierarchy/domain/service"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"
	"controlplane/internal/observability"

	"github.com/google/uuid"
)

type PersonalWorkspaceServiceImpl struct {
	repo    hierarchyRepoInterface.PersonalWorkspaceRepository
	metrics observability.WorkflowRecorder
}

func NewPersonalWorkspaceService(repo hierarchyRepoInterface.PersonalWorkspaceRepository, metrics observability.WorkflowRecorder) hierarchySvcInterface.PersonalWorkspaceService {
	return &PersonalWorkspaceServiceImpl{repo: repo, metrics: metrics}
}

func (s *PersonalWorkspaceServiceImpl) CreateWorkspaceForPersonal(ctx context.Context, in *hierarchyEntity.CreatePersonalWorkspace) (*hierarchyEntity.CreatePersonalWorkspace, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	workspaceID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate personal workspace id: %w", err)
	}
	now := time.Now().UTC()
	in.ID = workspaceID
	in.CreatedAt = now
	in.UpdatedAt = now

	out, err := s.repo.CreateWorkspaceForPersonal(ctx, in)
	if err != nil {
		switch {
		case errors.Is(err, hierarchyTaxonomy.ErrNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, hierarchyTaxonomy.ErrAlreadyExists):
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		case errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		return nil, err
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return out, nil
}

func (s *PersonalWorkspaceServiceImpl) ListWorkspacesForPersonal(ctx context.Context, in *hierarchyEntity.ListPersonalWorkspaces) ([]hierarchyEntity.ListPersonalWorkspaces, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	items, err := s.repo.ListWorkspacesForPersonal(ctx, in)
	if err != nil {
		return nil, err
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return items, nil
}

func (s *PersonalWorkspaceServiceImpl) ListWorkspaceCatalogForPersonal(ctx context.Context, in *hierarchyEntity.ListPersonalWorkspaceCatalog) ([]hierarchyEntity.ListPersonalWorkspaceCatalog, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	items, err := s.repo.ListWorkspaceCatalogForPersonal(ctx, in)
	if err != nil {
		return nil, err
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return items, nil
}

func (s *PersonalWorkspaceServiceImpl) DeleteWorkspaceForPersonal(ctx context.Context, in *hierarchyEntity.DeletePersonalWorkspace) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	err := s.repo.DeleteWorkspaceForPersonal(ctx, in)
	if err != nil {
		switch {
		case errors.Is(err, hierarchyTaxonomy.ErrNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		return err
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return nil
}
