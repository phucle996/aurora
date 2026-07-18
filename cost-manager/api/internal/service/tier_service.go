package service

import (
	"context"
	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
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
func (s *tierService) GetTiersList(ctx context.Context, page, limit int, serviceType entity.ServiceType, search string) ([]*entity.Tier, int64, error) {
	return s.tierRepo.ListTiers(ctx, page, limit, serviceType, search)
}

// GetTierDetail chuẩn hóa composite identity rồi lấy full aggregate cho màn Edit.
func (s *tierService) GetTierDetail(ctx context.Context, code string, serviceType entity.ServiceType) (*entity.TierDetail, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	return s.tierRepo.GetTierDetail(ctx, code, serviceType)
}

// UpdateTierMetadata sửa display metadata độc lập để không tạo pricing version ngoài ý muốn.
func (s *tierService) UpdateTierMetadata(ctx context.Context, update entity.TierMetadataUpdate) (*entity.TierMetadata, error) {
	update.Code = strings.TrimSpace(update.Code)
	update.Name = strings.TrimSpace(update.Name)
	if update.Code == "" || update.Name == "" || len(update.Name) > 128 || update.MetadataVersion < 1 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	return s.tierRepo.UpdateTierMetadata(ctx, update)
}

// CreateTierVersion chuẩn hóa full snapshot, validate invariant và tính checksum trước transaction.
func (s *tierService) CreateTierVersion(ctx context.Context, create entity.TierVersionCreate) (*entity.TierVersion, error) {
	create.Code = strings.TrimSpace(create.Code)
	create.ChangeReason = strings.TrimSpace(create.ChangeReason)
	if create.Code == "" || create.ChangeReason == "" ||
		len(create.ChangeReason) > 2_000 || create.ExpectedLatestVersion < 1 || create.CreatedBy == uuid.Nil ||
		create.EffectiveFrom.IsZero() || len(create.Ranges) > 1_000 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	// Không cho publish ngược thời gian; clock skew nhỏ được dung sai một phút.
	if create.EffectiveFrom.Before(time.Now().UTC().Add(-time.Minute)) {
		return nil, billingTaxonomy.ErrTierEffectiveConflict
	}

	// Copy trước khi sort để service không làm thay đổi slice thuộc caller.
	create.Ranges = append([]entity.TierRangeInput(nil), create.Ranges...)
	sort.Slice(create.Ranges, func(i, j int) bool {
		return create.Ranges[i].RangeStart < create.Ranges[j].RangeStart
	})

	// Một pricing schedule hợp lệ phải phủ liên tục từ zero đến đúng một infinity.
	if err := validateTierRanges(create.Ranges); err != nil {
		return nil, err
	}
	create.Checksum = tierVersionChecksum(create)
	return s.tierRepo.CreateTierVersion(ctx, create)
}

// tierVersionChecksum tạo content fingerprint ổn định để Engine kiểm tra snapshot đã load đầy đủ.
func tierVersionChecksum(create entity.TierVersionCreate) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00", create.Code, string(create.ServiceType))
	for _, tierRange := range create.Ranges {
		_, _ = fmt.Fprintf(h, "%d:%d:%d;", tierRange.RangeStart, tierRange.RangeEnd, tierRange.BaseUnitPrice)
	}
	return hex.EncodeToString(h.Sum(nil))
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
		if current.ID != uuid.Nil {
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
