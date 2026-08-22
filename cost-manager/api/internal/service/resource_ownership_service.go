package service

import (
	"context"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"
)

// resourceOwnershipService là Service điều phối việc ghi nhận và áp dụng sự kiện thay đổi quyền sở hữu tài nguyên (Resource Ownership Projection):
// - Nhận sự kiện đã xác thực hợp đồng từ Redis Consumer.
// - Chuyển giao xuống Repository để ghi nhận Inbox bất biến và cập nhật bảng `billing.resource_ownership_projections`.
type resourceOwnershipService struct {
	repo billingRepoInterface.ResourceOwnershipRepository
}

// NewResourceOwnershipService khởi tạo một instance mới của resourceOwnershipService, trả về interface ResourceOwnershipService.
func NewResourceOwnershipService(repo billingRepoInterface.ResourceOwnershipRepository) billingSvcInterface.ResourceOwnershipService {
	return &resourceOwnershipService{repo: repo}
}

// ProcessResourceOwnershipEvent xử lý sự kiện sở hữu tài nguyên:
// - Đảm bảo tính nguyên tử (Atomicity) và Idempotency thông qua Repository Layer.
// - Nếu sự kiện đã được ghi nhận trước đó (trùng event_id & payload_hash), Repository sẽ bỏ qua mà không tạo side-effect.
func (s *resourceOwnershipService) ProcessResourceOwnershipEvent(ctx context.Context, event *entity.ResourceOwnershipEvent) error {
	return s.repo.ApplyResourceOwnershipEvent(ctx, event)
}
