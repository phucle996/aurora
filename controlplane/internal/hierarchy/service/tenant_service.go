// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/service/tenant_service.go
//            Đặc Tả Nghiệp Vụ Quản Lý Vòng Đời Tenant
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

// [COMMENT]: TenantService triển khai TenantService interface với repo dependency
type TenantService struct {
	repo coreRepoInterface.TenantRepository
}

// [COMMENT]: NewTenantService tạo instance mới của TenantService
func NewTenantService(
	repo coreRepoInterface.TenantRepository,
) coreSvcInterface.TenantService {
	return &TenantService{
		repo: repo,
	}
}

// [COMMENT]: CreateTenant thực hiện tạo mới Tenant, sinh UUIDv7, ghi metrics
func (s *TenantService) CreateTenant(ctx context.Context, tenant coreEntity.Tenant, ownerID uuid.UUID) (*coreEntity.Tenant, error) {
	// [COMMENT]: Sinh UUIDv7 cho tenant mới
	tenantID, err := uuid.NewV7()
	if err != nil {
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, apperr.Wrap(coreTaxonomy.ErrGenUUID, err, coreMetric.OutcomeFailure)
	}

	now := time.Now().UTC()

	tenant.ID = tenantID
	tenant.Status = coreEntity.TenantStatusActive
	tenant.CreatedAt = now
	tenant.UpdatedAt = now

	// [COMMENT]: Gọi repo thực thi insert và đo latency
	start := time.Now()
	result, err := s.repo.CreateTenant(ctx, tenant, ownerID)
	duration := time.Since(start)
	if err != nil {
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "CreateTenant", coreMetric.OutcomeFailure, duration, err)
		coreMetric.ServiceCall(ctx, coreMetric.OutcomeFailure)
		return nil, err
	}

	// [COMMENT]: Ghi nhận thành công
	coreMetric.Downstream(ctx, coreMetric.KindRepo, "CreateTenant", coreMetric.OutcomeSuccess, duration, nil)
	coreMetric.ServiceCall(ctx, coreMetric.OutcomeSuccess)
	return result, nil
}

// ListTenantsPaged gọi xuống repository để lấy danh sách tenants phân trang cho Edge warmup
func (s *TenantService) ListTenantsPaged(ctx context.Context, limit, offset int) ([]coreEntity.Tenant, bool, error) {
	start := time.Now()
	list, hasMore, err := s.repo.ListTenantsPaged(ctx, limit, offset)
	duration := time.Since(start)

	if err != nil {
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "ListTenantsPaged", coreMetric.OutcomeFailure, duration, err)
		return nil, false, err
	}

	coreMetric.Downstream(ctx, coreMetric.KindRepo, "ListTenantsPaged", coreMetric.OutcomeSuccess, duration, nil)
	return list, hasMore, nil
}

// CheckMembership kiểm tra user có thuộc tenant không, dùng cho xác thực context switch.
func (s *TenantService) CheckMembership(ctx context.Context, tenantID, userID uuid.UUID) (bool, string, error) {
	start := time.Now()
	isMember, role, err := s.repo.CheckMembership(ctx, tenantID, userID)
	duration := time.Since(start)

	if err != nil {
		coreMetric.Downstream(ctx, coreMetric.KindRepo, "CheckMembership", coreMetric.OutcomeFailure, duration, err)
		return false, "", err
	}

	coreMetric.Downstream(ctx, coreMetric.KindRepo, "CheckMembership", coreMetric.OutcomeSuccess, duration, nil)
	return isMember, role, nil
}
