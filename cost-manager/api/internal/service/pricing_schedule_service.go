package service

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

const (
	storageBytesPerDecimalGB = int64(1_000_000_000)
	storageMicrosPerGB       = int64(1_000_000)
)

type pricingScheduleService struct {
	repo                billingRepoInterface.PricingScheduleRepository
	pricingCache        *pricingCache
	notifyPricingOutbox func()
}

func NewPricingScheduleService(repo billingRepoInterface.PricingScheduleRepository, redisClient *goredis.Client, notifier ...func()) billingSvcInterface.PricingScheduleService {
	var notify func()
	if len(notifier) > 0 {
		notify = notifier[0]
	}
	return &pricingScheduleService{
		repo: repo,
		pricingCache: &pricingCache{
			repo:        repo,
			redisClient: redisClient,
			l1:          make(map[string]pricingCacheItem),
		},
		notifyPricingOutbox: notify,
	}
}

func (s *pricingScheduleService) GetPricingSchedules(ctx context.Context, page, limit int, chargeKind entity.ChargeKindCode, search string) ([]*entity.PricingSchedule, int64, error) {
	return s.repo.ListPricingSchedules(ctx, page, limit, chargeKind, search)
}

func (s *pricingScheduleService) GetPricingScheduleDetail(ctx context.Context, code string) (*entity.PricingScheduleDetail, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	return s.repo.GetPricingScheduleDetail(ctx, code)
}

func (s *pricingScheduleService) ResolveStorageSnapshot(ctx context.Context, zoneID uuid.UUID, at time.Time) (*entity.PricingSnapshot, error) {
	if zoneID == uuid.Nil {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	return s.repo.GetActivePricingSnapshot(ctx, entity.ChargeKindStorageCapacity, &zoneID, at)
}

func (s *pricingScheduleService) EstimateStorage(ctx context.Context, capacityBytes int64, zoneID uuid.UUID) (*entity.StorageEstimate, error) {
	if capacityBytes <= 0 || capacityBytes > 1<<60 || zoneID == uuid.Nil {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	snapshot, err := s.pricingCache.get(ctx, entity.ChargeKindStorageCapacity, &zoneID, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	// The StorageUsageReportV1 wire unit is fixed-point decimal GB-hour. One
	// micro GB is 1,000 bytes, so the quote uses the same floor as settlement.
	capacityMicros := uint64(capacityBytes / (storageBytesPerDecimalGB / storageMicrosPerGB))
	hourly, err := chargeProgressiveQuantity(capacityMicros, snapshot.Brackets)
	if err != nil {
		return nil, err
	}
	return &entity.StorageEstimate{
		CapacityBytes:            capacityBytes,
		HourlyMicroUnits:         hourly,
		Currency:                 snapshot.Currency,
		PricingScheduleCode:      snapshot.Code,
		PricingScheduleID:        snapshot.PricingScheduleID,
		PricingScheduleVersionID: snapshot.VersionID,
		PricingVersion:           snapshot.VersionNumber,
		PricingChecksum:          snapshot.Checksum,
		PricingEffectiveFrom:     snapshot.EffectiveFrom,
		EstimatedAt:              time.Now().UTC(),
	}, nil
}

func (s *pricingScheduleService) RunPricingCacheInvalidation(ctx context.Context) {
	s.pricingCache.runInvalidation(ctx)
}

func (s *pricingScheduleService) UpdatePricingScheduleMetadata(ctx context.Context, update entity.PricingScheduleMetadataUpdate) (*entity.PricingSchedule, error) {
	update.ScheduleCode = strings.TrimSpace(update.ScheduleCode)
	update.DisplayName = strings.TrimSpace(update.DisplayName)
	if update.ScheduleCode == "" || update.DisplayName == "" || len(update.DisplayName) > 128 || update.MetadataVersion < 1 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	return s.repo.UpdatePricingScheduleMetadata(ctx, update)
}

func (s *pricingScheduleService) CreatePricingScheduleVersion(ctx context.Context, create entity.PricingScheduleVersionCreate) (*entity.PricingScheduleVersion, error) {
	create.ScheduleCode = strings.TrimSpace(create.ScheduleCode)
	create.ChangeReason = strings.TrimSpace(create.ChangeReason)
	if create.ScheduleCode == "" || create.ChangeReason == "" || len(create.ChangeReason) > 2_000 ||
		create.ExpectedLatestVersion < 1 || create.CreatedBy == uuid.Nil || create.EffectiveFrom.IsZero() || len(create.Brackets) > 1_000 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	if create.EffectiveFrom.Before(time.Now().UTC().Add(-time.Minute)) {
		return nil, billingTaxonomy.ErrPricingScheduleEffectiveConflict
	}

	detail, err := s.repo.GetPricingScheduleDetail(ctx, create.ScheduleCode)
	if err != nil {
		return nil, err
	}
	if detail.Schedule.PricingModel != entity.PricingModelProgressiveUnit {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	create.Brackets = append([]entity.ScalarBracketInput(nil), create.Brackets...)
	sort.Slice(create.Brackets, func(i, j int) bool {
		return create.Brackets[i].RangeStartQuantity < create.Brackets[j].RangeStartQuantity
	})
	if err := validateScalarBrackets(create.Brackets); err != nil {
		return nil, err
	}
	create.Checksum = pricingScheduleChecksum(detail.Schedule, create.ExpectedLatestVersion+1, create.EffectiveFrom, create.Brackets)
	version, err := s.repo.CreatePricingScheduleVersion(ctx, create)
	if err != nil {
		return nil, err
	}
	if s.notifyPricingOutbox != nil {
		s.notifyPricingOutbox()
	}
	return version, nil
}

func validateScalarBrackets(brackets []entity.ScalarBracketInput) error {
	if len(brackets) == 0 || brackets[0].RangeStartQuantity != 0 {
		return billingTaxonomy.ErrInvalidPricingBrackets
	}
	for index, bracket := range brackets {
		if bracket.RangeStartQuantity < 0 || bracket.PriceNumeratorMicroUnits < 0 || bracket.PriceDenominatorQuantity <= 0 {
			return billingTaxonomy.ErrInvalidPricingBrackets
		}
		if index == len(brackets)-1 {
			if bracket.RangeEndQuantity != nil {
				return billingTaxonomy.ErrInvalidPricingBrackets
			}
			continue
		}
		if bracket.RangeEndQuantity == nil || *bracket.RangeEndQuantity != brackets[index+1].RangeStartQuantity {
			return billingTaxonomy.ErrInvalidPricingBrackets
		}
	}
	return nil
}

// chargeProgressiveQuantity keeps all arithmetic exact and rounds only once at
// the ledger/quote boundary. A local rational calculation is required because
// each bracket may use a different explicit denominator.
func chargeProgressiveQuantity(quantity uint64, brackets []entity.ScalarBracketInput) (int64, error) {
	total := new(big.Rat)
	for _, bracket := range brackets {
		start := uint64(bracket.RangeStartQuantity)
		if quantity <= start {
			break
		}
		upper := quantity
		if bracket.RangeEndQuantity != nil && uint64(*bracket.RangeEndQuantity) < upper {
			upper = uint64(*bracket.RangeEndQuantity)
		}
		if upper <= start {
			continue
		}
		units := new(big.Int).SetUint64(upper - start)
		numerator := new(big.Int).Mul(units, big.NewInt(bracket.PriceNumeratorMicroUnits))
		total.Add(total, new(big.Rat).SetFrac(numerator, big.NewInt(bracket.PriceDenominatorQuantity)))
	}
	ceil := new(big.Int).Quo(total.Num(), total.Denom())
	if new(big.Int).Mod(total.Num(), total.Denom()).Sign() != 0 {
		ceil.Add(ceil, big.NewInt(1))
	}
	if !ceil.IsInt64() {
		return 0, fmt.Errorf("pricing charge exceeds BIGINT capacity")
	}
	return ceil.Int64(), nil
}

func pricingScheduleChecksum(schedule entity.PricingSchedule, version int, effectiveFrom time.Time, brackets []entity.ScalarBracketInput) string {
	hash := sha256.New()
	write := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	write(schedule.Code)
	write(string(schedule.ChargeKindCode))
	write(string(schedule.PricingModel))
	write(string(schedule.ScopeType))
	if schedule.ZoneID != nil && *schedule.ZoneID != uuid.Nil {
		write(schedule.ZoneID.String())
	}
	write(schedule.Currency)
	write(effectiveFrom.UTC().Format(time.RFC3339Nano))
	write(fmt.Sprintf("%d", version))
	for _, bracket := range brackets {
		write(fmt.Sprintf("%d", bracket.RangeStartQuantity))
		if bracket.RangeEndQuantity == nil {
			write("infinity")
		} else {
			write(fmt.Sprintf("%d", *bracket.RangeEndQuantity))
		}
		write(fmt.Sprintf("%d", bracket.PriceNumeratorMicroUnits))
		write(fmt.Sprintf("%d", bracket.PriceDenominatorQuantity))
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}
