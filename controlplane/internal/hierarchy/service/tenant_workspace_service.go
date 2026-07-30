// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/service/tenant_workspace_service.go
//            Đặc Tả Nghiệp Vụ Quản Lý Vòng Đời Workspace Doanh Nghiệp (Tenant Scope)
// ======================================================================================================

package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"controlplane/internal/cacheengine"
	entity "controlplane/internal/hierarchy/domain/entity"
	hierarchyrepo "controlplane/internal/hierarchy/domain/repo"
	hierarchyservice "controlplane/internal/hierarchy/domain/service"
	metrics "controlplane/internal/hierarchy/metrics"
	taxonomy "controlplane/internal/hierarchy/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
)

// [COMMENT]: TenantWorkspaceServiceImpl triển khai TenantWorkspaceService interface
type TenantWorkspaceServiceImpl struct {
	repo        hierarchyrepo.TenantWorkspaceRepository
	cacheEngine *cacheengine.CacheRegistry
}

// [COMMENT]: NewTenantWorkspaceService tạo instance mới của TenantWorkspaceService
func NewTenantWorkspaceService(
	repo hierarchyrepo.TenantWorkspaceRepository,
	cacheEngine *cacheengine.CacheRegistry,
) hierarchyservice.TenantWorkspaceService {
	return &TenantWorkspaceServiceImpl{
		repo:        repo,
		cacheEngine: cacheEngine,
	}
}

func (s *TenantWorkspaceServiceImpl) CreateWorkspaceForTenant(ctx context.Context, workspace entity.TenantWorkspace) (*entity.TenantWorkspace, error) {
	workspaceID, err := uuid.NewV7()
	if err != nil {
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, apperr.Wrap(taxonomy.ErrGenUUID, err, metrics.OutcomeFailure)
	}

	now := time.Now().UTC()
	workspace.ID = workspaceID
	workspace.CreatedAt = now
	workspace.UpdatedAt = now

	start := time.Now()
	result, err := s.repo.Create(ctx, workspace)
	duration := time.Since(start)
	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "CreateWorkspaceForTenant", metrics.OutcomeFailure, duration, err)
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, err
	}

	metrics.Downstream(ctx, metrics.KindRepo, "CreateWorkspaceForTenant", metrics.OutcomeSuccess, duration, nil)
	metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
	return result, nil
}

func (s *TenantWorkspaceServiceImpl) GetWorkspaceForTenant(ctx context.Context, workspaceID uuid.UUID) (*entity.TenantWorkspace, error) {
	start := time.Now()
	result, err := s.repo.GetByID(ctx, workspaceID)
	duration := time.Since(start)
	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "GetWorkspaceForTenant", metrics.OutcomeFailure, duration, err)
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, err
	}

	metrics.Downstream(ctx, metrics.KindRepo, "GetWorkspaceForTenant", metrics.OutcomeSuccess, duration, nil)
	metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
	return result, nil
}

func (s *TenantWorkspaceServiceImpl) ListWorkspacesForTenant(ctx context.Context, tenantID uuid.UUID, userID uuid.UUID, roleID uuid.UUID) ([]*entity.TenantWorkspace, error) {
	start := time.Now()

	// [COMMENT]: Lấy dữ liệu permissions từ L1 Cache (tenant_role) theo key <role_id>:<tenant_id>
	val, err := s.cacheEngine.GetOrLoad(ctx, "tenant_role", roleID.String()+":"+tenantID.String())
	if err != nil {
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, err
	}
	roleEntry, ok := val.(*iamproto.RoleEntry)
	if !ok {
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, errors.New("invalid cache entry type for tenant_role")
	}

	// [COMMENT]: Parse permission keys 5 cấp để trích xuất Workspace IDs có quyền read
	var wsIDs []uuid.UUID
	hasWildcard := false
	for _, p := range roleEntry.Permissions {
		if strings.HasSuffix(p, ":hierarchy:workspace:read") {
			parts := strings.Split(p, ":")
			if len(parts) >= 5 {
				wsIDStr := parts[1]
				if wsIDStr == "00000000-0000-0000-0000-000000000000" {
					hasWildcard = true
					break
				}
				if id, err := uuid.Parse(wsIDStr); err == nil {
					wsIDs = append(wsIDs, id)
				}
			}
		}
	}

	// [COMMENT]: Định tuyến Repo query dựa trên wildcard hoặc danh sách cụ thể
	var result []*entity.TenantWorkspace
	if hasWildcard {
		result, err = s.repo.ListAllByTenant(ctx, tenantID)
	} else {
		// [COMMENT]: Nếu không có quyền nào hoặc list trống, trả về empty slice thay vì query lỗi
		if len(wsIDs) == 0 {
			metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
			return []*entity.TenantWorkspace{}, nil
		}
		result, err = s.repo.ListByTenantAndIDs(ctx, tenantID, wsIDs)
	}

	duration := time.Since(start)
	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "ListWorkspacesForTenant", metrics.OutcomeFailure, duration, err)
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, err
	}

	metrics.Downstream(ctx, metrics.KindRepo, "ListWorkspacesForTenant", metrics.OutcomeSuccess, duration, nil)
	metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
	return result, nil
}

func (s *TenantWorkspaceServiceImpl) UpdateWorkspaceForTenant(ctx context.Context, workspace entity.TenantWorkspace) (*entity.TenantWorkspace, error) {
	start := time.Now()
	result, err := s.repo.Update(ctx, workspace)
	duration := time.Since(start)
	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "UpdateWorkspaceForTenant", metrics.OutcomeFailure, duration, err)
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, err
	}

	metrics.Downstream(ctx, metrics.KindRepo, "UpdateWorkspaceForTenant", metrics.OutcomeSuccess, duration, nil)
	metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
	return result, nil
}

func (s *TenantWorkspaceServiceImpl) DeleteWorkspaceForTenant(ctx context.Context, workspaceID uuid.UUID, tenantID uuid.UUID) error {
	start := time.Now()
	err := s.repo.Delete(ctx, workspaceID, tenantID)
	duration := time.Since(start)
	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "DeleteWorkspaceForTenant", metrics.OutcomeFailure, duration, err)
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return err
	}

	metrics.Downstream(ctx, metrics.KindRepo, "DeleteWorkspaceForTenant", metrics.OutcomeSuccess, duration, nil)
	metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
	return nil
}

// [COMMENT]: ListWorkspaceCatalogForTenant hot path catalog — phân giải L1 cache permission rồi gọi query tối giản 3 cột lọc theo zoneID
func (s *TenantWorkspaceServiceImpl) ListWorkspaceCatalogForTenant(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID, userID uuid.UUID, roleID uuid.UUID) ([]entity.WorkspaceCatalog, error) {
	start := time.Now()

	// [COMMENT]: Tra cứu cache tenant_role để lấy permissions của role trong tenant
	val, err := s.cacheEngine.GetOrLoad(ctx, "tenant_role", roleID.String()+":"+tenantID.String())
	if err != nil {
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, err
	}
	roleEntry, ok := val.(*iamproto.RoleEntry)
	if !ok {
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, errors.New("invalid cache entry type for tenant_role")
	}

	// [COMMENT]: Parse permission keys 5 cấp để trích xuất workspace IDs có quyền read
	var wsIDs []uuid.UUID
	hasWildcard := false
	for _, p := range roleEntry.Permissions {
		if strings.HasSuffix(p, ":hierarchy:workspace:read") {
			parts := strings.Split(p, ":")
			if len(parts) >= 5 {
				wsIDStr := parts[1]
				if wsIDStr == "00000000-0000-0000-0000-000000000000" {
					hasWildcard = true
					break
				}
				if id, err := uuid.Parse(wsIDStr); err == nil {
					wsIDs = append(wsIDs, id)
				}
			}
		}
	}

	// [COMMENT]: Gọi catalog query tối giản dựa trên wildcard hoặc danh sách IDs cụ thể
	var result []entity.WorkspaceCatalog
	if hasWildcard {
		result, err = s.repo.ListCatalogAllByTenant(ctx, tenantID, zoneID)
	} else {
		if len(wsIDs) == 0 {
			metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
			return []entity.WorkspaceCatalog{}, nil
		}
		result, err = s.repo.ListCatalogByTenantAndIDs(ctx, tenantID, zoneID, wsIDs)
	}

	duration := time.Since(start)
	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "ListWorkspaceCatalogForTenant", metrics.OutcomeFailure, duration, err)
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, err
	}

	metrics.Downstream(ctx, metrics.KindRepo, "ListWorkspaceCatalogForTenant", metrics.OutcomeSuccess, duration, nil)
	metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
	return result, nil
}
