package coreRepoInterface

import (
	"context"
	coreEntity "controlplane/internal/hierarchy/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: TenantRepository định nghĩa giao diện truy cập dữ liệu cho Tenant
type TenantRepository interface {
	// CreateTenant tạo tenant mới và tự động thêm owner làm member đầu tiên trong cùng transaction/statement
	CreateTenant(ctx context.Context, tenant coreEntity.Tenant, ownerID uuid.UUID) (*coreEntity.Tenant, error)
}
