package hierarchySvcImpl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/cacheengine"
	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchyRepoInterface "controlplane/internal/hierarchy/domain/repo"
	hierarchySvcInterface "controlplane/internal/hierarchy/domain/service"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/internal/observability"

	"github.com/google/uuid"
)

type TenantWorkspaceServiceImpl struct {
	repo        hierarchyRepoInterface.TenantWorkspaceRepository
	cacheEngine *cacheengine.CacheRegistry
	metrics     observability.WorkflowRecorder
}

func NewTenantWorkspaceService(repo hierarchyRepoInterface.TenantWorkspaceRepository, cacheEngine *cacheengine.CacheRegistry, metrics observability.WorkflowRecorder) hierarchySvcInterface.TenantWorkspaceService {
	return &TenantWorkspaceServiceImpl{repo: repo, cacheEngine: cacheEngine, metrics: metrics}
}

func (s *TenantWorkspaceServiceImpl) CreateWorkspaceForTenant(ctx context.Context, in *hierarchyEntity.CreateTenantWorkspace) (*hierarchyEntity.CreateTenantWorkspace, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	workspaceID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate tenant workspace id: %w", err)
	}
	now := time.Now().UTC()
	in.ID = workspaceID
	in.CreatedAt = now
	in.UpdatedAt = now

	out, err := s.repo.CreateWorkspaceForTenant(ctx, in)
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

func (s *TenantWorkspaceServiceImpl) ListWorkspacesForTenant(ctx context.Context, in *hierarchyEntity.ListTenantWorkspaces) ([]hierarchyEntity.ListTenantWorkspaces, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	value, err := s.cacheEngine.GetOrLoad(ctx, "membership_role", in.ActorUserID.String()+":"+in.TenantID.String())
	if err != nil {
		return nil, err
	}
	roleEntry, ok := value.(*iamproto.RoleEntry)
	if !ok {
		return nil, errors.New("invalid cache entry type for membership_role")
	}

	in.AllowedWorkspaceIDs = make([]uuid.UUID, 0)
	for _, permission := range roleEntry.Permissions {
		if !strings.HasSuffix(permission, ":hierarchy:workspace:read") {
			continue
		}
		parts := strings.Split(permission, ":")
		if len(parts) < 5 {
			continue
		}
		if parts[1] == uuid.Nil.String() {
			in.AllWorkspaces = true
			in.AllowedWorkspaceIDs = nil
			break
		}
		workspaceID, parseErr := uuid.Parse(parts[1])
		if parseErr == nil {
			in.AllowedWorkspaceIDs = append(in.AllowedWorkspaceIDs, workspaceID)
		}
	}

	items, err := s.repo.ListWorkspacesForTenant(ctx, in)
	if err != nil {
		return nil, err
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return items, nil
}

func (s *TenantWorkspaceServiceImpl) ListWorkspaceCatalogForTenant(ctx context.Context, in *hierarchyEntity.ListTenantWorkspaceCatalog) ([]hierarchyEntity.ListTenantWorkspaceCatalog, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	value, err := s.cacheEngine.GetOrLoad(ctx, "membership_role", in.ActorUserID.String()+":"+in.TenantID.String())
	if err != nil {
		return nil, err
	}
	roleEntry, ok := value.(*iamproto.RoleEntry)
	if !ok {
		return nil, errors.New("invalid cache entry type for membership_role")
	}

	in.AllowedWorkspaceIDs = make([]uuid.UUID, 0)
	for _, permission := range roleEntry.Permissions {
		if !strings.HasSuffix(permission, ":hierarchy:workspace:read") {
			continue
		}
		parts := strings.Split(permission, ":")
		if len(parts) < 5 {
			continue
		}
		if parts[1] == uuid.Nil.String() {
			in.AllWorkspaces = true
			in.AllowedWorkspaceIDs = nil
			break
		}
		workspaceID, parseErr := uuid.Parse(parts[1])
		if parseErr == nil {
			in.AllowedWorkspaceIDs = append(in.AllowedWorkspaceIDs, workspaceID)
		}
	}

	items, err := s.repo.ListWorkspaceCatalogForTenant(ctx, in)
	if err != nil {
		return nil, err
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return items, nil
}

func (s *TenantWorkspaceServiceImpl) DeleteWorkspaceForTenant(ctx context.Context, in *hierarchyEntity.DeleteTenantWorkspace) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	err := s.repo.DeleteWorkspaceForTenant(ctx, in)
	if err != nil {
		if errors.Is(err, hierarchyTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		} else if errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed) {
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		return err
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return nil
}
