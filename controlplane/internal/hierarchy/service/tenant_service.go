// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/service/tenant_service.go
//            Đặc Tả Nghiệp Vụ Quản Lý Vòng Đời Tenant
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

// [COMMENT]: TenantService triển khai TenantService interface với repo dependency
type TenantService struct {
	repo                hierarchyrepo.TenantRepository
	notifyBillingOutbox func()
}

// SetBillingOutboxNotifier is wired once by the app composition root before
// HTTP serving starts. The notifier is only a latency hint; the PostgreSQL
// outbox and relay reconciliation remain the recovery boundary.
func (s *TenantService) SetBillingOutboxNotifier(notify func()) {
	s.notifyBillingOutbox = notify
}

// [COMMENT]: NewTenantService tạo instance mới của TenantService
func NewTenantService(
	repo hierarchyrepo.TenantRepository,
) hierarchyservice.TenantService {
	return &TenantService{
		repo: repo,
	}
}

// [COMMENT]: CreateTenant thực hiện tạo mới Tenant, sinh UUIDv7, ghi metrics
func (s *TenantService) CreateTenant(ctx context.Context, tenant entity.Tenant, ownerID uuid.UUID) (*entity.Tenant, error) {
	// [COMMENT]: Sinh UUIDv7 cho tenant mới
	tenantID, err := uuid.NewV7()
	if err != nil {
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, apperr.Wrap(taxonomy.ErrGenUUID, err, metrics.OutcomeFailure)
	}

	now := time.Now().UTC()

	tenant.ID = tenantID
	tenant.Status = entity.TenantStatusActive
	tenant.CreatedAt = now
	tenant.UpdatedAt = now

	// [COMMENT]: Gọi repo thực thi insert và đo latency
	start := time.Now()
	result, err := s.repo.CreateTenant(ctx, tenant, ownerID)
	duration := time.Since(start)
	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "CreateTenant", metrics.OutcomeFailure, duration, err)
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, err
	}

	// [COMMENT]: Ghi nhận thành công
	metrics.Downstream(ctx, metrics.KindRepo, "CreateTenant", metrics.OutcomeSuccess, duration, nil)
	metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
	if s.notifyBillingOutbox != nil {
		s.notifyBillingOutbox()
	}
	return result, nil
}

// ResolveTenantByDomain gọi xuống repository để tìm kiếm Tenant theo domain liên kết
func (s *TenantService) ResolveTenantByDomain(ctx context.Context, domain string) (*entity.Tenant, error) {
	start := time.Now()
	result, err := s.repo.ResolveTenantByDomain(ctx, domain)
	duration := time.Since(start)

	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "ResolveTenantByDomain", metrics.OutcomeFailure, duration, err)
		return nil, err
	}

	metrics.Downstream(ctx, metrics.KindRepo, "ResolveTenantByDomain", metrics.OutcomeSuccess, duration, nil)
	return result, nil
}

// ListTenantsPaged gọi xuống repository để lấy danh sách tenants phân trang cho Edge warmup
func (s *TenantService) ListTenantsPaged(ctx context.Context, limit, offset int) ([]entity.Tenant, bool, error) {
	start := time.Now()
	list, hasMore, err := s.repo.ListTenantsPaged(ctx, limit, offset)
	duration := time.Since(start)

	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "ListTenantsPaged", metrics.OutcomeFailure, duration, err)
		return nil, false, err
	}

	metrics.Downstream(ctx, metrics.KindRepo, "ListTenantsPaged", metrics.OutcomeSuccess, duration, nil)
	return list, hasMore, nil
}

// CheckMembership kiểm tra user có thuộc tenant không, dùng cho xác thực context switch.
func (s *TenantService) CheckMembership(ctx context.Context, tenantID, userID uuid.UUID) (bool, string, error) {
	start := time.Now()
	isMember, role, err := s.repo.CheckMembership(ctx, tenantID, userID)
	duration := time.Since(start)

	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "CheckMembership", metrics.OutcomeFailure, duration, err)
		return false, "", err
	}

	metrics.Downstream(ctx, metrics.KindRepo, "CheckMembership", metrics.OutcomeSuccess, duration, nil)
	return isMember, role, nil
}
