package zoneSvcInterface

import (
	"context"
	coreEntity "controlplane/internal/hierarchy/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: TenantService định nghĩa giao diện nghiệp vụ cho Tenant
type TenantService interface {
	// CreateTenant thực hiện tạo mới một Tenant cùng các ràng buộc liên quan
	CreateTenant(ctx context.Context, tenant coreEntity.Tenant, ownerID uuid.UUID) (*coreEntity.Tenant, error)
	// ResolveTenantByDomain tìm Tenant theo Domain của nó
	ResolveTenantByDomain(ctx context.Context, domain string) (*coreEntity.Tenant, error)
	// ListTenantsPaged lấy danh sách Tenants phân trang (chunk) để warmup
	ListTenantsPaged(ctx context.Context, limit, offset int) ([]coreEntity.Tenant, bool, error)
	// CheckMembership kiểm tra user có thuộc tenant không, trả về role nếu có
	CheckMembership(ctx context.Context, tenantID, userID uuid.UUID) (isMember bool, role string, err error)
}
