/*
============================================================================
MAP: BILLING SERVICE LAYER - LIFECYCLE SERVICE
============================================================================
CONTRACT:
1. Xử lý nghiệp vụ thuần túy cho sự kiện chuyển giao vòng đời tài nguyên.
2. Điều phối ResourceOwnershipRepository để cập nhật Inbox và Ownership Projection (Validate dữ liệu đã thực hiện tại Handler Layer).
============================================================================
*/

package service

import (
	"context"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
)

type ResourceOwnershipService interface {
	ProcessResourceOwnershipEvent(ctx context.Context, event *entity.ResourceOwnershipEvent) error
}

type resourceOwnershipService struct {
	repo billingRepoInterface.ResourceOwnershipRepository
}

// [COMMENT]: NewResourceOwnershipService khởi tạo service cập nhật ownership projection.
func NewResourceOwnershipService(repo billingRepoInterface.ResourceOwnershipRepository) ResourceOwnershipService {
	return &resourceOwnershipService{repo: repo}
}

// [COMMENT]: ProcessLifecycleEvent xử lý business logic và chuyển giao sự kiện xuống Repository Layer.
func (s *resourceOwnershipService) ProcessResourceOwnershipEvent(ctx context.Context, event *entity.ResourceOwnershipEvent) error {
	if err := s.repo.ApplyResourceOwnershipEvent(ctx, event); err != nil {
		return err
	}
	return nil
}
