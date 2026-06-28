// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/service/workspace_service.go
//            Đặc Tả Nghiệp Vụ Quản Lý Vòng Đời Workspace
// ======================================================================================================
//
// 📜 THIẾT KẾ:
//   - WorkspaceService validate input, sinh UUIDv7, đo metrics OTel và ủy quyền cho repo insert.
//   - Không chứa logic cache — chỉ tập trung vào nghiệp vụ và truy cập DB.
//
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

// [COMMENT]: WorkspaceService triển khai WorkspaceService interface với repo dependency
type WorkspaceService struct {
	repo coreRepoInterface.WorkspaceRepository
}

// [COMMENT]: NewWorkspaceService tạo instance mới của WorkspaceService
func NewWorkspaceService(
	repo coreRepoInterface.WorkspaceRepository,
) coreSvcInterface.WorkspaceService {
	return &WorkspaceService{
		repo: repo,
	}
}

// [COMMENT]: CreateWorkspace tạo workspace mới — validate, sinh UUIDv7, đo metrics, gọi repo
func (s *WorkspaceService) CreateWorkspace(ctx context.Context, input coreEntity.CreateWorkspaceInput) (*coreEntity.Workspace, error) {
	// [COMMENT]: Sinh UUIDv7 cho workspace mới
	workspaceID, err := uuid.NewV7()
	if err != nil {
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, apperr.Wrap(coreTaxonomy.ErrGenUUID, err, coreMetric.OutcomeFailure)
	}

	now := time.Now().UTC()

	// [COMMENT]: Xây dựng entity workspace với trạng thái mặc định active
	workspace := coreEntity.Workspace{
		ID:        workspaceID,
		Name:      input.Name,
		Status:    coreEntity.WorkspaceStatusActive,
		ZoneID:    input.ZoneID,
		TenantID:  input.TenantID,
		OwnerID:   input.OwnerID,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// [COMMENT]: Gọi repo để insert và đo latency downstream
	start := time.Now()
	result, err := s.repo.CreateWorkspace(ctx, workspace)
	duration := time.Since(start)
	if err != nil {
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "CreateWorkspace", coreMetric.OutcomeFailure, duration, err)
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, err
	}

	// [COMMENT]: Ghi nhận thành công
	coreMetric.Downstream(ctx, coreMetric.KindRepo, "CreateWorkspace", coreMetric.OutcomeSuccess, duration, nil)
	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return result, nil
}
