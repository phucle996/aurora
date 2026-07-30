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
	hierarchyMetrics "controlplane/internal/hierarchy/metrics"
	iamproto "controlplane/internal/iam/transport/rpc/proto"

	"github.com/google/uuid"
)

type TenantWorkspaceServiceImpl struct {
	repo        hierarchyRepoInterface.TenantWorkspaceRepository
	cacheEngine *cacheengine.CacheRegistry
}

func NewTenantWorkspaceService(repo hierarchyRepoInterface.TenantWorkspaceRepository, cacheEngine *cacheengine.CacheRegistry) hierarchySvcInterface.TenantWorkspaceService {
	return &TenantWorkspaceServiceImpl{repo: repo, cacheEngine: cacheEngine}
}

func (s *TenantWorkspaceServiceImpl) CreateWorkspaceForTenant(ctx context.Context, in *hierarchyEntity.CreateTenantWorkspace) (*hierarchyEntity.CreateTenantWorkspace, error) {
	workspaceID, err := uuid.NewV7()
	if err != nil {
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return nil, fmt.Errorf("generate tenant workspace id: %w", err)
	}
	now := time.Now().UTC()
	in.ID = workspaceID
	in.CreatedAt = now
	in.UpdatedAt = now

	startedAt := time.Now()
	out, err := s.repo.CreateWorkspaceForTenant(ctx, in)
	if err != nil {
		hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "CreateWorkspaceForTenant", hierarchyMetrics.OutcomeFailure, time.Since(startedAt), err)
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return nil, err
	}
	hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "CreateWorkspaceForTenant", hierarchyMetrics.OutcomeSuccess, time.Since(startedAt), nil)
	hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeSuccess)
	return out, nil
}

func (s *TenantWorkspaceServiceImpl) ListWorkspacesForTenant(ctx context.Context, in *hierarchyEntity.ListTenantWorkspaces) ([]hierarchyEntity.ListTenantWorkspaces, error) {
	startedAt := time.Now()
	value, err := s.cacheEngine.GetOrLoad(ctx, "tenant_role", in.RoleID.String()+":"+in.TenantID.String())
	if err != nil {
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return nil, err
	}
	roleEntry, ok := value.(*iamproto.RoleEntry)
	if !ok {
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return nil, errors.New("invalid cache entry type for tenant_role")
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
		hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "ListWorkspacesForTenant", hierarchyMetrics.OutcomeFailure, time.Since(startedAt), err)
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return nil, err
	}
	hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "ListWorkspacesForTenant", hierarchyMetrics.OutcomeSuccess, time.Since(startedAt), nil)
	hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeSuccess)
	return items, nil
}

func (s *TenantWorkspaceServiceImpl) ListWorkspaceCatalogForTenant(ctx context.Context, in *hierarchyEntity.ListTenantWorkspaceCatalog) ([]hierarchyEntity.ListTenantWorkspaceCatalog, error) {
	startedAt := time.Now()
	value, err := s.cacheEngine.GetOrLoad(ctx, "tenant_role", in.RoleID.String()+":"+in.TenantID.String())
	if err != nil {
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return nil, err
	}
	roleEntry, ok := value.(*iamproto.RoleEntry)
	if !ok {
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return nil, errors.New("invalid cache entry type for tenant_role")
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
		hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "ListWorkspaceCatalogForTenant", hierarchyMetrics.OutcomeFailure, time.Since(startedAt), err)
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return nil, err
	}
	hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "ListWorkspaceCatalogForTenant", hierarchyMetrics.OutcomeSuccess, time.Since(startedAt), nil)
	hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeSuccess)
	return items, nil
}

func (s *TenantWorkspaceServiceImpl) DeleteWorkspaceForTenant(ctx context.Context, in *hierarchyEntity.DeleteTenantWorkspace) error {
	startedAt := time.Now()
	err := s.repo.DeleteWorkspaceForTenant(ctx, in)
	if err != nil {
		hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "DeleteWorkspaceForTenant", hierarchyMetrics.OutcomeFailure, time.Since(startedAt), err)
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return err
	}
	hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "DeleteWorkspaceForTenant", hierarchyMetrics.OutcomeSuccess, time.Since(startedAt), nil)
	hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeSuccess)
	return nil
}
