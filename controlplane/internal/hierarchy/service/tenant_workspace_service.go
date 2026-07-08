// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/service/tenant_workspace_service.go
//            Đặc Tả Nghiệp Vụ Quản Lý Vòng Đời Workspace Doanh Nghiệp (Tenant Scope)
// ======================================================================================================

package zoneSvcImpl

import (
	"context"
	"errors"
	"strings"
	"time"

	"controlplane/internal/cacheengine"
	coreEntity "controlplane/internal/hierarchy/domain/entity"
	coreRepoInterface "controlplane/internal/hierarchy/domain/repo"
	coreSvcInterface "controlplane/internal/hierarchy/domain/service"
	coreMetric "controlplane/internal/hierarchy/metrics"
	coreTaxonomy "controlplane/internal/hierarchy/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
)

// [COMMENT]: TenantWorkspaceServiceImpl triển khai TenantWorkspaceService interface
type TenantWorkspaceServiceImpl struct {
	repo        coreRepoInterface.TenantWorkspaceRepository
	cacheEngine *cacheengine.CacheRegistry
}

// [COMMENT]: NewTenantWorkspaceService tạo instance mới của TenantWorkspaceService
func NewTenantWorkspaceService(
	repo coreRepoInterface.TenantWorkspaceRepository,
	cacheEngine *cacheengine.CacheRegistry,
) coreSvcInterface.TenantWorkspaceService {
	return &TenantWorkspaceServiceImpl{
		repo:        repo,
		cacheEngine: cacheEngine,
	}
}

func (s *TenantWorkspaceServiceImpl) CreateWorkspaceForTenant(ctx context.Context, workspace coreEntity.Workspace) (*coreEntity.Workspace, error) {
	workspaceID, err := uuid.NewV7()
	if err != nil {
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, apperr.Wrap(coreTaxonomy.ErrGenUUID, err, coreMetric.OutcomeFailure)
	}

	now := time.Now().UTC()
	workspace.ID = workspaceID
	workspace.CreatedAt = now
	workspace.UpdatedAt = now

	start := time.Now()
	result, err := s.repo.Create(ctx, workspace)
	duration := time.Since(start)
	if err != nil {
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "CreateWorkspaceForTenant", coreMetric.OutcomeFailure, duration, err)
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, err
	}

	coreMetric.Downstream(ctx, coreMetric.KindRepo, "CreateWorkspaceForTenant", coreMetric.OutcomeSuccess, duration, nil)
	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return result, nil
}

func (s *TenantWorkspaceServiceImpl) GetWorkspaceForTenant(ctx context.Context, workspaceID uuid.UUID) (*coreEntity.Workspace, error) {
	start := time.Now()
	result, err := s.repo.GetByID(ctx, workspaceID)
	duration := time.Since(start)
	if err != nil {
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "GetWorkspaceForTenant", coreMetric.OutcomeFailure, duration, err)
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, err
	}

	coreMetric.Downstream(ctx, coreMetric.KindRepo, "GetWorkspaceForTenant", coreMetric.OutcomeSuccess, duration, nil)
	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return result, nil
}

func (s *TenantWorkspaceServiceImpl) ListWorkspacesForTenant(ctx context.Context, tenantID uuid.UUID, userID uuid.UUID, roleID uuid.UUID) ([]*coreEntity.Workspace, error) {
	start := time.Now()

	// [COMMENT]: Lấy dữ liệu permissions từ L1 Cache (tenant_role) theo key <role_id>:<tenant_id>
	val, err := s.cacheEngine.GetOrLoad(ctx, "tenant_role", roleID.String()+":"+tenantID.String())
	if err != nil {
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, err
	}
	roleEntry, ok := val.(*iamproto.RoleEntry)
	if !ok {
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
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
	var result []*coreEntity.Workspace
	if hasWildcard {
		result, err = s.repo.ListAllByTenant(ctx, tenantID)
	} else {
		// [COMMENT]: Nếu không có quyền nào hoặc list trống, trả về empty slice thay vì query lỗi
		if len(wsIDs) == 0 {
			coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
			return []*coreEntity.Workspace{}, nil
		}
		result, err = s.repo.ListByTenantAndIDs(ctx, tenantID, wsIDs)
	}

	duration := time.Since(start)
	if err != nil {
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "ListWorkspacesForTenant", coreMetric.OutcomeFailure, duration, err)
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, err
	}

	coreMetric.Downstream(ctx, coreMetric.KindRepo, "ListWorkspacesForTenant", coreMetric.OutcomeSuccess, duration, nil)
	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return result, nil
}

func (s *TenantWorkspaceServiceImpl) UpdateWorkspaceForTenant(ctx context.Context, workspace coreEntity.Workspace) (*coreEntity.Workspace, error) {
	start := time.Now()
	result, err := s.repo.Update(ctx, workspace)
	duration := time.Since(start)
	if err != nil {
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "UpdateWorkspaceForTenant", coreMetric.OutcomeFailure, duration, err)
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, err
	}

	coreMetric.Downstream(ctx, coreMetric.KindRepo, "UpdateWorkspaceForTenant", coreMetric.OutcomeSuccess, duration, nil)
	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return result, nil
}

func (s *TenantWorkspaceServiceImpl) DeleteWorkspaceForTenant(ctx context.Context, workspaceID uuid.UUID) error {
	start := time.Now()
	err := s.repo.Delete(ctx, workspaceID)
	duration := time.Since(start)
	if err != nil {
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "DeleteWorkspaceForTenant", coreMetric.OutcomeFailure, duration, err)
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return err
	}

	coreMetric.Downstream(ctx, coreMetric.KindRepo, "DeleteWorkspaceForTenant", coreMetric.OutcomeSuccess, duration, nil)
	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return nil
}

// [COMMENT]: ListWorkspaceCatalogForTenant hot path catalog — phân giải L1 cache permission rồi gọi query tối giản 3 cột lọc theo zoneID
func (s *TenantWorkspaceServiceImpl) ListWorkspaceCatalogForTenant(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID, userID uuid.UUID, roleID uuid.UUID) ([]coreEntity.WorkspaceCatalog, error) {
	start := time.Now()

	// [COMMENT]: Tra cứu cache tenant_role để lấy permissions của role trong tenant
	val, err := s.cacheEngine.GetOrLoad(ctx, "tenant_role", roleID.String()+":"+tenantID.String())
	if err != nil {
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, err
	}
	roleEntry, ok := val.(*iamproto.RoleEntry)
	if !ok {
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
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
	var result []coreEntity.WorkspaceCatalog
	if hasWildcard {
		result, err = s.repo.ListCatalogAllByTenant(ctx, tenantID, zoneID)
	} else {
		if len(wsIDs) == 0 {
			coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
			return []coreEntity.WorkspaceCatalog{}, nil
		}
		result, err = s.repo.ListCatalogByTenantAndIDs(ctx, tenantID, zoneID, wsIDs)
	}

	duration := time.Since(start)
	if err != nil {
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "ListWorkspaceCatalogForTenant", coreMetric.OutcomeFailure, duration, err)
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, err
	}

	coreMetric.Downstream(ctx, coreMetric.KindRepo, "ListWorkspaceCatalogForTenant", coreMetric.OutcomeSuccess, duration, nil)
	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return result, nil
}

