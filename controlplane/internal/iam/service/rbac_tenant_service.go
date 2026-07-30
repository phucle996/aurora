package iamSvcImpl

import (
	"context"
	"errors"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/observability"

	"github.com/google/uuid"
)

// [COMMENT]: RbacTenantService thực thi interface quản lý vai trò trong phạm vi tenant
type RbacTenantService struct {
	repo    iamRepoInterface.RbacTenantRepository
	metrics observability.WorkflowRecorder
}

// [COMMENT]: NewRbacTenantService khởi tạo một thể hiện mới của RbacTenantService
func NewRbacTenantService(repo iamRepoInterface.RbacTenantRepository, metrics observability.WorkflowRecorder) iamSvcInterface.RbacTenantService {
	return &RbacTenantService{
		repo:    repo,
		metrics: metrics,
	}
}

// [COMMENT]: ListTenantRoles lấy danh sách vai trò của một tenant
func (s *RbacTenantService) ListTenantRoles(ctx context.Context, tenantID uuid.UUID) (out []iamEntity.Role, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, iamTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.ListTenantRoles(ctx, tenantID)
}

// [COMMENT]: AssignUserRole gán vai trò trong phạm vi tenant cho user (skeleton)
func (s *RbacTenantService) AssignUserRole(ctx context.Context, userRole *iamEntity.UserRole) error {
	// [COMMENT]: Sẽ hiện thực hóa ở phase tiếp theo
	return nil
}

// [COMMENT]: AssignTenantRole gán vai trò trong phạm vi tenant cho tenant con (skeleton)
func (s *RbacTenantService) AssignTenantRole(ctx context.Context, tenantRole *iamEntity.TenantRole) error {
	// [COMMENT]: Sẽ hiện thực hóa ở phase tiếp theo
	return nil
}
