// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/service/personal_workspace_service.go
//            Đặc Tả Nghiệp Vụ Quản Lý Vòng Đời Workspace Cá Nhân (Personal Scope)
// ======================================================================================================

package service

import (
	"context"
	"time"

	entity "controlplane/internal/hierarchy/domain/entity"
	hierarchyrepo "controlplane/internal/hierarchy/domain/repo"
	hierarchyservice "controlplane/internal/hierarchy/domain/service"
	metrics "controlplane/internal/hierarchy/metrics"
	taxonomy "controlplane/internal/hierarchy/taxonomy"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
)

// [COMMENT]: PersonalWorkspaceServiceImpl triển khai PersonalWorkspaceService interface
type PersonalWorkspaceServiceImpl struct {
	repo hierarchyrepo.PersonalWorkspaceRepository
}

// [COMMENT]: NewPersonalWorkspaceService tạo instance mới của PersonalWorkspaceService
func NewPersonalWorkspaceService(
	repo hierarchyrepo.PersonalWorkspaceRepository,
) hierarchyservice.PersonalWorkspaceService {
	return &PersonalWorkspaceServiceImpl{
		repo: repo,
	}
}

func (s *PersonalWorkspaceServiceImpl) CreateWorkspaceForPersonal(ctx context.Context, workspace entity.PersonalWorkspace) (*entity.PersonalWorkspace, error) {
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
		metrics.Downstream(ctx, metrics.KindRepo, "CreateWorkspaceForPersonal", metrics.OutcomeFailure, duration, err)
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, err
	}

	metrics.Downstream(ctx, metrics.KindRepo, "CreateWorkspaceForPersonal", metrics.OutcomeSuccess, duration, nil)
	metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
	return result, nil
}

func (s *PersonalWorkspaceServiceImpl) GetWorkspaceForPersonal(ctx context.Context, workspaceID uuid.UUID) (*entity.PersonalWorkspace, error) {
	start := time.Now()
	result, err := s.repo.GetByID(ctx, workspaceID)
	duration := time.Since(start)
	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "GetWorkspaceForPersonal", metrics.OutcomeFailure, duration, err)
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, err
	}

	metrics.Downstream(ctx, metrics.KindRepo, "GetWorkspaceForPersonal", metrics.OutcomeSuccess, duration, nil)
	metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
	return result, nil
}

func (s *PersonalWorkspaceServiceImpl) ListWorkspacesForPersonal(ctx context.Context, userID uuid.UUID) ([]*entity.WorkspacePersonalListItem, error) {
	start := time.Now()
	result, err := s.repo.ListByOwner(ctx, userID)
	duration := time.Since(start)
	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "ListWorkspacesForPersonal", metrics.OutcomeFailure, duration, err)
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, err
	}

	metrics.Downstream(ctx, metrics.KindRepo, "ListWorkspacesForPersonal", metrics.OutcomeSuccess, duration, nil)
	metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
	return result, nil
}

func (s *PersonalWorkspaceServiceImpl) UpdateWorkspaceForPersonal(ctx context.Context, workspace entity.PersonalWorkspace) (*entity.PersonalWorkspace, error) {
	start := time.Now()
	result, err := s.repo.Update(ctx, workspace)
	duration := time.Since(start)
	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "UpdateWorkspaceForPersonal", metrics.OutcomeFailure, duration, err)
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, err
	}

	metrics.Downstream(ctx, metrics.KindRepo, "UpdateWorkspaceForPersonal", metrics.OutcomeSuccess, duration, nil)
	metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
	return result, nil
}

func (s *PersonalWorkspaceServiceImpl) DeleteWorkspaceForPersonal(ctx context.Context, workspaceID uuid.UUID, ownerID uuid.UUID) error {
	start := time.Now()
	err := s.repo.Delete(ctx, workspaceID, ownerID)
	duration := time.Since(start)
	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "DeleteWorkspaceForPersonal", metrics.OutcomeFailure, duration, err)
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return err
	}

	metrics.Downstream(ctx, metrics.KindRepo, "DeleteWorkspaceForPersonal", metrics.OutcomeSuccess, duration, nil)
	metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
	return nil
}

// [COMMENT]: ListWorkspaceCatalogForPersonal hot path catalog — query trực tiếp theo owner_id + zone_id, không cần cache
func (s *PersonalWorkspaceServiceImpl) ListWorkspaceCatalogForPersonal(ctx context.Context, userID uuid.UUID, zoneID uuid.UUID) ([]entity.WorkspaceCatalog, error) {
	start := time.Now()
	result, err := s.repo.ListCatalogByOwner(ctx, userID, zoneID)
	duration := time.Since(start)
	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "ListWorkspaceCatalogForPersonal", metrics.OutcomeFailure, duration, err)
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, err
	}

	metrics.Downstream(ctx, metrics.KindRepo, "ListWorkspaceCatalogForPersonal", metrics.OutcomeSuccess, duration, nil)
	metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
	return result, nil
}
