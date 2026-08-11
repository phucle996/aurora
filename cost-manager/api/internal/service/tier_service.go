package service

import (
	"context"
	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"crypto/sha256"
	"fmt"
	"math/bits"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

const (
	storageBytesPerDecimalGB     int64 = 1_000_000_000
	storageMicrosPerGB           int64 = 1_000_000
	storageEstimateHoursPerMonth int64 = 730
)

type tierService struct {
	tierRepo            billingRepoInterface.TierRepository
	pricingCache        *pricingCache
	notifyPricingOutbox func()
}

// [COMMENT]: NewTierService khởi tạo instance của tierService.
func NewTierService(tierRepo billingRepoInterface.TierRepository, redisClient *goredis.Client, notifier ...func()) billingSvcInterface.TierService {
	var notify func()
	if len(notifier) > 0 {
		notify = notifier[0]
	}
	return &tierService{
		tierRepo: tierRepo,
		pricingCache: &pricingCache{
			repo:        tierRepo,
			redisClient: redisClient,
			l1:          make(map[entity.ServiceType]pricingCacheItem),
		},
		notifyPricingOutbox: notify,
	}
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

// EstimateStorage uses the same effective progressive decimal GB-hour ranges
// as the billing engine, then scales one hourly charge by the documented
// 730-hour month. The estimate never writes wallet/ledger state.
func (s *tierService) EstimateStorage(ctx context.Context, capacityBytes int64) (*entity.StorageEstimate, error) {
	snapshot, err := s.pricingCache.get(ctx, entity.ServiceTypeStorage)
	if err != nil {
		return nil, err
	}
	// The engine reports fixed-point GB-hours: one micro-unit is 1,000 bytes
	// for a one-hour capacity observation. Floor here to match the wire
	// contract; the pricing snapshot performs the final integer rounding.
	capacityMicros := capacityBytes / (storageBytesPerDecimalGB / storageMicrosPerGB)
	wholeGB := capacityMicros / storageMicrosPerGB
	remainderMicros := capacityMicros % storageMicrosPerGB
	var numeratorHigh, numeratorLow uint64
	for _, tierRange := range snapshot.Ranges {
		if tierRange.RangeStart > wholeGB || (tierRange.RangeStart == wholeGB && remainderMicros == 0) {
			break
		}
		startMicros := tierRange.RangeStart * storageMicrosPerGB
		upperMicros := capacityMicros
		if tierRange.RangeEnd != 0 && tierRange.RangeEnd <= wholeGB {
			upperMicros = tierRange.RangeEnd * storageMicrosPerGB
		}
		unitsMicros := upperMicros - startMicros
		productHigh, productLow := bits.Mul64(uint64(unitsMicros), uint64(tierRange.BaseUnitPrice))
		var carry uint64
		numeratorLow, carry = bits.Add64(numeratorLow, productLow, 0)
		var overflow uint64
		numeratorHigh, overflow = bits.Add64(numeratorHigh, productHigh, carry)
		if overflow != 0 {
			return nil, fmt.Errorf("pricing charge exceeds uint128 capacity")
		}
	}
	const storageMicrosDivisor uint64 = 1_000_000
	if numeratorHigh >= storageMicrosDivisor {
		return nil, fmt.Errorf("pricing charge exceeds BIGINT capacity")
	}
	highQuotient, highRemainder := bits.Div64(0, numeratorHigh, storageMicrosDivisor)
	quotientHigh := highQuotient
	quotientLow, remainder := bits.Div64(highRemainder, numeratorLow, storageMicrosDivisor)
	const maxInt64 = uint64(^uint64(0) >> 1)
	if quotientHigh != 0 || quotientLow > maxInt64 {
		return nil, fmt.Errorf("pricing charge exceeds BIGINT capacity")
	}
	if remainder != 0 {
		if quotientLow == maxInt64 {
			return nil, fmt.Errorf("pricing charge exceeds BIGINT capacity")
		}
		quotientLow++
	}
	hourly := int64(quotientLow)
	if hourly > 0 && uint64(hourly) > maxInt64/uint64(storageEstimateHoursPerMonth) {
		return nil, fmt.Errorf("storage estimate exceeds BIGINT capacity")
	}
	monthly := hourly * storageEstimateHoursPerMonth
	return &entity.StorageEstimate{
		CapacityBytes:        capacityBytes,
		HourlyMicroUnits:     hourly,
		MonthlyMicroUnits:    monthly,
		BillingHoursPerMonth: storageEstimateHoursPerMonth,
		Currency:             snapshot.Currency,
		TierCode:             snapshot.Code,
		TierID:               snapshot.TierID,
		TierVersionID:        snapshot.TierVersionID,
		PricingVersion:       snapshot.VersionNumber,
		PricingChecksum:      snapshot.Checksum,
		PricingEffectiveFrom: snapshot.EffectiveFrom,
		EstimatedAt:          time.Now().UTC(),
	}, nil
}

func (s *tierService) RunPricingCacheInvalidation(ctx context.Context) {
	s.pricingCache.runInvalidation(ctx)
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
	if len(create.Ranges) == 0 || create.Ranges[0].RangeStart != 0 {
		return nil, billingTaxonomy.ErrInvalidTierRanges
	}
	seenIDs := make(map[uuid.UUID]struct{}, len(create.Ranges))
	for i, current := range create.Ranges {
		if current.RangeStart < 0 || current.BaseUnitPrice < 0 {
			return nil, billingTaxonomy.ErrInvalidTierRanges
		}
		if current.RangeEnd != 0 && current.RangeEnd <= current.RangeStart {
			return nil, billingTaxonomy.ErrInvalidTierRanges
		}

		// UUID nil dành cho insert; UUID hiện hữu không được lặp trong cùng payload.
		if current.ID != uuid.Nil {
			if _, exists := seenIDs[current.ID]; exists {
				return nil, billingTaxonomy.ErrInvalidTierRanges
			}
			seenIDs[current.ID] = struct{}{}
		}

		isLast := i == len(create.Ranges)-1
		if isLast {
			if current.RangeEnd != 0 {
				return nil, billingTaxonomy.ErrInvalidTierRanges
			}
			continue
		}
		// Infinity chỉ được nằm cuối; equality giữa hai boundary loại bỏ cả gap lẫn overlap.
		if current.RangeEnd == 0 || current.RangeEnd != create.Ranges[i+1].RangeStart {
			return nil, billingTaxonomy.ErrInvalidTierRanges
		}
	}

	checksum := sha256.New()
	_, _ = fmt.Fprintf(checksum, "%s\x00%s\x00", create.Code, string(create.ServiceType))
	for _, tierRange := range create.Ranges {
		_, _ = fmt.Fprintf(checksum, "%d:%d:%d;", tierRange.RangeStart, tierRange.RangeEnd, tierRange.BaseUnitPrice)
	}
	create.Checksum = fmt.Sprintf("%x", checksum.Sum(nil))
	version, err := s.tierRepo.CreateTierVersion(ctx, create)
	if err != nil {
		return nil, err
	}
	// [COMMENT]: Repo đã commit version + outbox trước khi wake; mất wake vẫn được reconciliation thu hồi.
	if s.notifyPricingOutbox != nil {
		s.notifyPricingOutbox()
	}
	return version, nil
}
