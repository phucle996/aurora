package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

// [COMMENT]: PersonalRenderContextService định nghĩa interface cho business logic đọc render context platform/cá nhân
type PersonalRenderContextService interface {
	// [COMMENT]: GetPersonalRenderContext truy vấn quyền từ L1 cache và tính toán projection capabilities/navigation cho personal session
	GetPersonalRenderContext(ctx context.Context, workflow *iamEntity.PersonalRenderContext) error
}
