/*
============================================================================
MAP: BILLING SERVICE LAYER - LIFECYCLE SERVICE
============================================================================
CONTRACT:
1. Xử lý nghiệp vụ thuần túy cho sự kiện chuyển giao vòng đời tài nguyên.
2. Điều phối LifecycleRepository để cập nhật Inbox và Ownership Projection (Validate dữ liệu đã thực hiện tại Handler Layer).
============================================================================
*/

package service

import (
	"context"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
)

type LifecycleService interface {
	ProcessLifecycleEvent(ctx context.Context, event *entity.LifecycleEvent) error
}

type lifecycleService struct {
	repo billingRepoInterface.LifecycleRepository
}

// [COMMENT]: NewLifecycleService khởi tạo Service Layer xử lý nghiệp vụ vòng đời tài nguyên.
func NewLifecycleService(repo billingRepoInterface.LifecycleRepository) LifecycleService {
	return &lifecycleService{repo: repo}
}

// [COMMENT]: ProcessLifecycleEvent xử lý business logic và chuyển giao sự kiện xuống Repository Layer.
func (s *lifecycleService) ProcessLifecycleEvent(ctx context.Context, event *entity.LifecycleEvent) error {
	if err := s.repo.ApplyLifecycleEvent(ctx, event); err != nil {
		return err
	}
	return nil
}
