// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/service/personal_workspace_service.go
//            Đặc Tả Nghiệp Vụ Quản Lý Vòng Đời Workspace Cá Nhân (Personal Scope)
// ======================================================================================================

package zoneSvcImpl

import (
	"context"
	"time"

	coreEntity "controlplane/internal/hierarchy/domain/entity"
	coreRepoInterface "controlplane/internal/hierarchy/domain/repo"
	coreSvcInterface "controlplane/internal/hierarchy/domain/service"
	coreMetric "controlplane/internal/hierarchy/metrics"
	coreTaxonomy "controlplane/internal/hierarchy/taxonomy"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
)

// [COMMENT]: PersonalWorkspaceServiceImpl triển khai PersonalWorkspaceService interface
type PersonalWorkspaceServiceImpl struct {
	repo coreRepoInterface.PersonalWorkspaceRepository
}

// [COMMENT]: NewPersonalWorkspaceService tạo instance mới của PersonalWorkspaceService
func NewPersonalWorkspaceService(
	repo coreRepoInterface.PersonalWorkspaceRepository,
) coreSvcInterface.PersonalWorkspaceService {
	return &PersonalWorkspaceServiceImpl{
		repo: repo,
	}
}

func (s *PersonalWorkspaceServiceImpl) CreateWorkspaceForPersonal(ctx context.Context, workspace coreEntity.Workspace) (*coreEntity.Workspace, error) {
	workspaceID, err := uuid.NewV7()
	if err != nil {
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, apperr.Wrap(coreTaxonomy.ErrGenUUID, err, coreMetric.OutcomeFailure)
	}

	now := time.Now().UTC()
	workspace.ID = workspaceID
	workspace.Status = coreEntity.WorkspaceStatusActive
	workspace.CreatedAt = now
	workspace.UpdatedAt = now

	start := time.Now()
	result, err := s.repo.Create(ctx, workspace)
	duration := time.Since(start)
	if err != nil {
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "CreateWorkspaceForPersonal", coreMetric.OutcomeFailure, duration, err)
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, err
	}

	coreMetric.Downstream(ctx, coreMetric.KindRepo, "CreateWorkspaceForPersonal", coreMetric.OutcomeSuccess, duration, nil)
	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return result, nil
}

func (s *PersonalWorkspaceServiceImpl) GetWorkspaceForPersonal(ctx context.Context, workspaceID uuid.UUID) (*coreEntity.Workspace, error) {
	start := time.Now()
	result, err := s.repo.GetByID(ctx, workspaceID)
	duration := time.Since(start)
	if err != nil {
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "GetWorkspaceForPersonal", coreMetric.OutcomeFailure, duration, err)
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, err
	}

	coreMetric.Downstream(ctx, coreMetric.KindRepo, "GetWorkspaceForPersonal", coreMetric.OutcomeSuccess, duration, nil)
	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return result, nil
}

func (s *PersonalWorkspaceServiceImpl) ListWorkspacesForPersonal(ctx context.Context, userID uuid.UUID) ([]*coreEntity.Workspace, error) {
	start := time.Now()
	result, err := s.repo.ListByOwner(ctx, userID)
	duration := time.Since(start)
	if err != nil {
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "ListWorkspacesForPersonal", coreMetric.OutcomeFailure, duration, err)
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, err
	}

	coreMetric.Downstream(ctx, coreMetric.KindRepo, "ListWorkspacesForPersonal", coreMetric.OutcomeSuccess, duration, nil)
	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return result, nil
}

func (s *PersonalWorkspaceServiceImpl) UpdateWorkspaceForPersonal(ctx context.Context, workspace coreEntity.Workspace) (*coreEntity.Workspace, error) {
	start := time.Now()
	result, err := s.repo.Update(ctx, workspace)
	duration := time.Since(start)
	if err != nil {
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "UpdateWorkspaceForPersonal", coreMetric.OutcomeFailure, duration, err)
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, err
	}

	coreMetric.Downstream(ctx, coreMetric.KindRepo, "UpdateWorkspaceForPersonal", coreMetric.OutcomeSuccess, duration, nil)
	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return result, nil
}

func (s *PersonalWorkspaceServiceImpl) DeleteWorkspaceForPersonal(ctx context.Context, workspaceID uuid.UUID) error {
	start := time.Now()
	err := s.repo.Delete(ctx, workspaceID)
	duration := time.Since(start)
	if err != nil {
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "DeleteWorkspaceForPersonal", coreMetric.OutcomeFailure, duration, err)
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return err
	}

	coreMetric.Downstream(ctx, coreMetric.KindRepo, "DeleteWorkspaceForPersonal", coreMetric.OutcomeSuccess, duration, nil)
	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return nil
}

// [COMMENT]: ListWorkspaceCatalogForPersonal hot path catalog — query trực tiếp theo owner_id + zone_id, không cần cache
func (s *PersonalWorkspaceServiceImpl) ListWorkspaceCatalogForPersonal(ctx context.Context, userID uuid.UUID, zoneID uuid.UUID) ([]coreEntity.WorkspaceCatalog, error) {
	start := time.Now()
	result, err := s.repo.ListCatalogByOwner(ctx, userID, zoneID)
	duration := time.Since(start)
	if err != nil {
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "ListWorkspaceCatalogForPersonal", coreMetric.OutcomeFailure, duration, err)
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, err
	}

	coreMetric.Downstream(ctx, coreMetric.KindRepo, "ListWorkspaceCatalogForPersonal", coreMetric.OutcomeSuccess, duration, nil)
	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return result, nil
}
