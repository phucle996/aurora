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
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

const (
	pricingChecksumTimeLayout           = "2006-01-02T15:04:05.000000Z07:00"
	storageAdjustmentChecksumTimeLayout = "2006-01-02T15:04:05.000000Z07:00"
)

type pricingScheduleListService struct {
	repo billingRepoInterface.PricingScheduleListRepository
}
type pricingScheduleDetailService struct {
	repo billingRepoInterface.PricingScheduleDetailRepository
}
type storageEstimateService struct {
	adjustmentRepo billingRepoInterface.StorageZoneAdjustmentRepository
	pricingCache   *pricingCache
}
type pricingScheduleMetadataService struct {
	repo billingRepoInterface.PricingScheduleMetadataRepository
}
type pricingScheduleVersionPublishService struct {
	repo   billingRepoInterface.PricingScheduleVersionPublishRepository
	notify func()
}
type storageZoneAdjustmentPublishService struct {
	repo billingRepoInterface.StorageZoneAdjustmentRepository
}

func NewPricingScheduleListService(repo billingRepoInterface.PricingScheduleListRepository) *pricingScheduleListService {
	return &pricingScheduleListService{repo: repo}
}
func NewPricingScheduleDetailService(repo billingRepoInterface.PricingScheduleDetailRepository) *pricingScheduleDetailService {
	return &pricingScheduleDetailService{repo: repo}
}
func NewStorageEstimateService(snapshotRepo billingRepoInterface.PricingSnapshotRepository, adjustmentRepo billingRepoInterface.StorageZoneAdjustmentRepository, redisClient *goredis.Client) *storageEstimateService {
	return &storageEstimateService{adjustmentRepo: adjustmentRepo, pricingCache: &pricingCache{repo: snapshotRepo, redisClient: redisClient, l1: make(map[string]pricingCacheItem)}}
}
func NewPricingScheduleMetadataService(repo billingRepoInterface.PricingScheduleMetadataRepository) *pricingScheduleMetadataService {
	return &pricingScheduleMetadataService{repo: repo}
}
func NewPricingScheduleVersionPublishService(repo billingRepoInterface.PricingScheduleVersionPublishRepository, notify func()) *pricingScheduleVersionPublishService {
	return &pricingScheduleVersionPublishService{repo: repo, notify: notify}
}
func NewStorageZoneAdjustmentPublishService(repo billingRepoInterface.StorageZoneAdjustmentRepository) *storageZoneAdjustmentPublishService {
	return &storageZoneAdjustmentPublishService{repo: repo}
}

func (s *pricingScheduleListService) GetPricingSchedules(ctx context.Context, page, limit int, chargeKind entity.ChargeKindCode, search string) ([]*entity.PricingScheduleListItem, int64, error) {
	return s.repo.ListPricingSchedules(ctx, page, limit, chargeKind, search)
}

func (s *pricingScheduleDetailService) GetPricingScheduleDetail(ctx context.Context, code string) (*entity.PricingScheduleDetail, []entity.PricingScheduleDetailBracket, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, nil, billingTaxonomy.ErrInvalidArgument
	}
	return s.repo.GetPricingScheduleDetail(ctx, code)
}

func (s *storageEstimateService) EstimateStorage(ctx context.Context, capacityBytes int64, zoneID uuid.UUID) (*entity.StorageEstimate, error) {
	if capacityBytes <= 0 || capacityBytes > 1<<60 || zoneID == uuid.Nil {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	now := time.Now().UTC()
	snapshot, err := s.pricingCache.get(ctx, entity.ChargeKindStorageCapacity, now)
	if err != nil {
		return nil, err
	}
	adjustment, err := s.adjustmentRepo.GetActiveStorageZonePriceAdjustment(ctx, zoneID, now)
	if err != nil {
		return nil, err
	}
	if adjustment != nil && storageZoneAdjustmentChecksum(adjustment.ZoneID, adjustment.VersionNumber, adjustment.EffectiveFrom, adjustment.MultiplierNumerator, adjustment.MultiplierDenominator) != adjustment.Checksum {
		return nil, fmt.Errorf("Storage Zone price adjustment checksum mismatch")
	}

	// The Storage adapter observes one exact byte-hour for every occupied byte
	// in the fixed one-hour window. Do not round quantity before the generic
	// PAYG money boundary.
	capacityByteHours := uint64(capacityBytes)
	adjustmentNumerator, adjustmentDenominator := int64(1), int64(1)
	if adjustment != nil {
		adjustmentNumerator = adjustment.MultiplierNumerator
		adjustmentDenominator = adjustment.MultiplierDenominator
	}
	hourly, err := storageEstimateCharge(capacityByteHours, snapshot.Brackets, adjustmentNumerator, adjustmentDenominator)
	if err != nil {
		return nil, err
	}
	var adjustmentID *uuid.UUID
	var adjustmentVersion *int
	var adjustmentChecksum *string
	if adjustment != nil {
		adjustmentID = &adjustment.ID
		adjustmentVersion = &adjustment.VersionNumber
		adjustmentChecksum = &adjustment.Checksum
		adjustmentNumerator = adjustment.MultiplierNumerator
		adjustmentDenominator = adjustment.MultiplierDenominator
	}
	return &entity.StorageEstimate{
		CapacityBytes:             capacityBytes,
		HourlyMicroUnits:          hourly,
		Currency:                  snapshot.Currency,
		PricingScheduleCode:       snapshot.Code,
		PricingScheduleID:         snapshot.PricingScheduleID,
		PricingScheduleVersionID:  snapshot.VersionID,
		PricingVersion:            snapshot.VersionNumber,
		PricingChecksum:           snapshot.Checksum,
		PricingEffectiveFrom:      snapshot.EffectiveFrom,
		RateAdjustmentID:          adjustmentID,
		RateAdjustmentVersion:     adjustmentVersion,
		RateAdjustmentChecksum:    adjustmentChecksum,
		RateAdjustmentNumerator:   adjustmentNumerator,
		RateAdjustmentDenominator: adjustmentDenominator,
		EstimatedAt:               time.Now().UTC(),
	}, nil
}

func (s *storageEstimateService) RunPricingCacheInvalidation(ctx context.Context) {
	s.pricingCache.runInvalidation(ctx)
}

func (s *pricingScheduleMetadataService) UpdatePricingScheduleMetadata(ctx context.Context, update entity.PricingScheduleMetadataCommand) (*entity.PricingScheduleMetadataUpdated, error) {
	update.ScheduleCode = strings.TrimSpace(update.ScheduleCode)
	update.DisplayName = strings.TrimSpace(update.DisplayName)
	if update.ScheduleCode == "" || update.DisplayName == "" || len(update.DisplayName) > 128 || update.MetadataVersion < 1 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	return s.repo.UpdatePricingScheduleMetadata(ctx, update)
}

func (s *pricingScheduleVersionPublishService) CreatePricingScheduleVersion(ctx context.Context, create entity.PricingScheduleVersionPublishCommand, brackets []entity.PricingScheduleVersionPublishBracket) (*entity.PricingScheduleVersionPublished, []entity.PricingScheduleVersionPublishBracket, error) {
	create.ScheduleCode = strings.TrimSpace(create.ScheduleCode)
	create.ChangeReason = strings.TrimSpace(create.ChangeReason)
	// PostgreSQL timestamptz persists microseconds. Normalize before hashing and
	// inserting so every runtime verifies the exact durable timestamp bytes.
	create.EffectiveFrom = create.EffectiveFrom.UTC().Truncate(time.Microsecond)
	if create.ScheduleCode == "" || create.ChangeReason == "" || len(create.ChangeReason) > 2_000 ||
		create.ExpectedLatestVersion < 0 || create.CreatedBy == uuid.Nil || create.EffectiveFrom.IsZero() || len(brackets) > 1_000 {
		return nil, nil, billingTaxonomy.ErrInvalidArgument
	}
	if create.EffectiveFrom.Before(time.Now().UTC().Add(-time.Minute)) {
		return nil, nil, billingTaxonomy.ErrPricingScheduleEffectiveConflict
	}

	target, err := s.repo.GetPricingScheduleVersionPublishTarget(ctx, create.ScheduleCode)
	if err != nil {
		return nil, nil, err
	}
	if target.PricingModel != entity.PricingModelProgressiveUnit {
		return nil, nil, billingTaxonomy.ErrInvalidArgument
	}
	brackets = append([]entity.PricingScheduleVersionPublishBracket(nil), brackets...)
	sort.Slice(brackets, func(i, j int) bool {
		return brackets[i].RangeStartQuantity < brackets[j].RangeStartQuantity
	})
	if err := validatePricingScheduleVersionPublishBrackets(brackets); err != nil {
		return nil, nil, err
	}
	create.Checksum = pricingSchedulePublishChecksum(*target, create.ExpectedLatestVersion+1, create.EffectiveFrom, brackets)
	version, err := s.repo.CreatePricingScheduleVersion(ctx, create, brackets)
	if err != nil {
		return nil, nil, err
	}
	if s.notify != nil {
		s.notify()
	}
	return version, brackets, nil
}

func validatePricingScheduleVersionPublishBrackets(brackets []entity.PricingScheduleVersionPublishBracket) error {
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

// storageEstimateCharge keeps all arithmetic exact and rounds only once at
// the ledger/quote boundary. A local rational calculation is required because
// each bracket may use a different explicit denominator.
func storageEstimateCharge(quantity uint64, brackets []entity.PricingSnapshotBracket, adjustmentNumerator, adjustmentDenominator int64) (int64, error) {
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
	if adjustmentNumerator != 1 || adjustmentDenominator != 1 {
		if adjustmentNumerator < 0 || adjustmentDenominator <= 0 {
			return 0, billingTaxonomy.ErrInvalidArgument
		}
		total.Mul(total, new(big.Rat).SetFrac(big.NewInt(adjustmentNumerator), big.NewInt(adjustmentDenominator)))
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

func pricingSchedulePublishChecksum(target entity.PricingScheduleVersionPublishTarget, version int, effectiveFrom time.Time, brackets []entity.PricingScheduleVersionPublishBracket) string {
	hash := sha256.New()
	write := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	write(target.ScheduleCode)
	write(string(target.ChargeKindCode))
	write(string(target.PricingModel))
	write(target.Currency)
	write(effectiveFrom.UTC().Format(pricingChecksumTimeLayout))
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

func (s *storageZoneAdjustmentPublishService) CreateStorageZonePriceAdjustment(ctx context.Context, create entity.StorageZoneAdjustmentPublishCommand) (*entity.StorageZoneAdjustmentPublished, error) {
	create.ChangeReason = strings.TrimSpace(create.ChangeReason)
	create.EffectiveFrom = create.EffectiveFrom.UTC().Truncate(time.Microsecond)
	if create.ZoneID == uuid.Nil || create.CreatedBy == uuid.Nil || create.ExpectedLatestVersion < 0 ||
		create.EffectiveFrom.IsZero() || create.ChangeReason == "" || len(create.ChangeReason) > 2_000 ||
		create.MultiplierNumerator < 0 || create.MultiplierDenominator <= 0 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	if create.EffectiveFrom.Before(time.Now().UTC().Add(-time.Minute)) {
		return nil, billingTaxonomy.ErrStorageZoneAdjustmentConflict
	}
	create.Checksum = storageZoneAdjustmentChecksum(create.ZoneID, create.ExpectedLatestVersion+1, create.EffectiveFrom, create.MultiplierNumerator, create.MultiplierDenominator)
	return s.repo.CreateStorageZonePriceAdjustment(ctx, create)
}

func storageZoneAdjustmentChecksum(zoneID uuid.UUID, version int, effectiveFrom time.Time, numerator, denominator int64) string {
	hash := sha256.New()
	for _, value := range []string{
		zoneID.String(), fmt.Sprintf("%d", version),
		effectiveFrom.UTC().Format(storageAdjustmentChecksumTimeLayout),
		fmt.Sprintf("%d", numerator), fmt.Sprintf("%d", denominator),
	} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}
