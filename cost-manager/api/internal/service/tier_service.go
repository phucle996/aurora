package service

import (
	"context"
	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"sort"
	"strings"
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

// UpdateTier chuẩn hóa input và bảo vệ invariant của cả tập ranges trước khi mở transaction ghi.
func (s *tierService) UpdateTier(ctx context.Context, update entity.TierUpdate) (*entity.TierAggregate, error) {
	// Identity phải được client gửi chính xác; repository chỉ lookup và không mutate hai field này.
	update.Code = strings.TrimSpace(update.Code)
	update.ServiceType = strings.TrimSpace(update.ServiceType)
	update.Name = strings.TrimSpace(update.Name)
	if update.Code == "" || update.ServiceType == "" || update.Name == "" || update.Version < 1 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}

	// Copy trước khi sort để service không làm thay đổi slice thuộc caller.
	update.Ranges = append([]entity.TierRangeInput(nil), update.Ranges...)
	sort.Slice(update.Ranges, func(i, j int) bool {
		return update.Ranges[i].RangeStart < update.Ranges[j].RangeStart
	})

	// Một pricing schedule hợp lệ phải phủ liên tục từ zero đến đúng một infinity.
	if err := validateTierRanges(update.Ranges); err != nil {
		return nil, err
	}
	return s.tierRepo.UpdateTier(ctx, update)
}

// validateTierRanges áp dụng boundary contract [start,end), với end=0 biểu thị infinity.
func validateTierRanges(ranges []entity.TierRangeInput) error {
	if len(ranges) == 0 || ranges[0].RangeStart != 0 {
		return billingTaxonomy.ErrInvalidTierRanges
	}

	seenIDs := make(map[string]struct{}, len(ranges))
	for i, current := range ranges {
		if current.RangeStart < 0 || current.BaseUnitPrice < 0 {
			return billingTaxonomy.ErrInvalidTierRanges
		}
		if current.RangeEnd != 0 && current.RangeEnd <= current.RangeStart {
			return billingTaxonomy.ErrInvalidTierRanges
		}

		// UUID nil dành cho insert; UUID hiện hữu không được lặp trong cùng payload.
		if current.ID.String() != "00000000-0000-0000-0000-000000000000" {
			key := current.ID.String()
			if _, exists := seenIDs[key]; exists {
				return billingTaxonomy.ErrInvalidTierRanges
			}
			seenIDs[key] = struct{}{}
		}

		isLast := i == len(ranges)-1
		if isLast {
			if current.RangeEnd != 0 {
				return billingTaxonomy.ErrInvalidTierRanges
			}
			continue
		}
		// Infinity chỉ được nằm cuối; equality giữa hai boundary loại bỏ cả gap lẫn overlap.
		if current.RangeEnd == 0 || current.RangeEnd != ranges[i+1].RangeStart {
			return billingTaxonomy.ErrInvalidTierRanges
		}
	}
	return nil
}
