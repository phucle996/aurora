package repo

import (
	"context"
	entity "controlplane/internal/hierarchy/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: TenantRepository định nghĩa giao diện truy cập dữ liệu cho Tenant
type TenantRepository interface {
	// CreateTenant tạo tenant mới và tự động thêm owner làm member đầu tiên trong cùng transaction/statement
	CreateTenant(ctx context.Context, tenant entity.Tenant, ownerID uuid.UUID) (*entity.Tenant, error)
	// ResolveTenantByDomain tìm tenant theo domain (để phục vụ gRPC ResolveTenant từ Edge)
	ResolveTenantByDomain(ctx context.Context, domain string) (*entity.Tenant, error)
	// ListTenantsPaged lấy danh sách tenant phân trang theo offset/limit để warmup chunk
	ListTenantsPaged(ctx context.Context, limit, offset int) ([]entity.Tenant, bool, error)
	// CheckMembership kiểm tra user có thuộc thành viên của tenant không và trả về role tương ứng
	CheckMembership(ctx context.Context, tenantID, userID uuid.UUID) (isMember bool, role string, err error)
}
