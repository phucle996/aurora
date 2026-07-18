package service

import (
	"context"
	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"
)

type tierService struct {
	tierRepo billingRepoInterface.TierRepository
}

// [COMMENT]: NewTierService khởi tạo instance của tierService.
func NewTierService(tierRepo billingRepoInterface.TierRepository) billingSvcInterface.TierService {
	return &tierService{tierRepo: tierRepo}
}

// [COMMENT]: GetTiersList điều hướng gọi repository để lấy danh sách biểu giá.
// Không validate lại dữ liệu phân trang do việc làm sạch/validate đã được xử lý tập trung tại handler.
func (s *tierService) GetTiersList(ctx context.Context, page, limit int, serviceType, search string) ([]*entity.Tier, int64, error) {
	return s.tierRepo.ListTiers(ctx, page, limit, serviceType, search)
}
