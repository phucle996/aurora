package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

// [COMMENT]: TenantRenderContextService định nghĩa interface cho business logic đọc render context tenant
type TenantRenderContextService interface {
	// [COMMENT]: GetTenantRenderContext truy vấn membership permissions từ L1/L2 và tính toán capabilities/navigation cho tenant context
	GetTenantRenderContext(ctx context.Context, workflow *iamEntity.TenantRenderContext) error
}
