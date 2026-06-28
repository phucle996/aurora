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
}
