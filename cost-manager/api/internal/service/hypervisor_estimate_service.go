package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"cost-manager/api/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

const (
	hypervisorPricingReadinessStream = "billing.pricing.hypervisor.rateability.changed.v1"
	hypervisorPricingReadinessTTL    = 45 * time.Second
)

type hypervisorPricingReadinessPayload struct {
	SchemaVersion int      `json:"schema_version"`
	Ready         bool     `json:"ready"`
	Missing       []string `json:"missing"`
	ObservedAt    string   `json:"observed_at"`
	ValidUntil    string   `json:"valid_until"`
	Fingerprint   string   `json:"fingerprint"`
}

type hypervisorEstimateService struct {
	adjustmentRepo billingRepoInterface.HypervisorZoneAdjustmentRepository
	pricingCache   *pricingCache
}

func NewHypervisorEstimateService(snapshotRepo billingRepoInterface.PricingSnapshotRepository, adjustmentRepo billingRepoInterface.HypervisorZoneAdjustmentRepository, redisClient *goredis.Client) *hypervisorEstimateService {
	return &hypervisorEstimateService{
		adjustmentRepo: adjustmentRepo,
		pricingCache: &pricingCache{
			repo: snapshotRepo, redisClient: redisClient, l1: make(map[string]pricingCacheItem),
		},
	}
}

func (s *hypervisorEstimateService) EstimateHypervisor(ctx context.Context, cpuCores, memoryMIB, diskGIB int64, zoneID uuid.UUID) (*entity.HypervisorEstimate, error) {
	if cpuCores < 1 || cpuCores > 1024 || memoryMIB < 1 || memoryMIB > 4_194_304 || diskGIB < 1 || diskGIB > 1_048_576 || zoneID == uuid.Nil {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	now := time.Now().UTC()
	vcpu, err := s.pricingCache.get(ctx, entity.ChargeKindHypervisorVCPU, now)
	if err != nil {
		return nil, err
	}
	memory, err := s.pricingCache.get(ctx, entity.ChargeKindHypervisorMemoryMIB, now)
	if err != nil {
		return nil, err
	}
	disk, err := s.pricingCache.get(ctx, entity.ChargeKindHypervisorDiskGIB, now)
	if err != nil {
		return nil, err
	}
	if vcpu.ModuleCode != "hypervisor" || vcpu.RawInputUnit != "CORE_SECOND" ||
		memory.ModuleCode != "hypervisor" || memory.RawInputUnit != "MIB_SECOND" ||
		disk.ModuleCode != "hypervisor" || disk.RawInputUnit != "GIB_SECOND" ||
		vcpu.Currency != memory.Currency || vcpu.Currency != disk.Currency {
		return nil, fmt.Errorf("Hypervisor pricing snapshot contract mismatch")
	}
	adjustment, err := s.adjustmentRepo.GetActiveHypervisorZonePriceAdjustment(ctx, zoneID, now)
	if err != nil {
		return nil, err
	}
	numerator, denominator := int64(1), int64(1)
	if adjustment != nil {
		if hypervisorZoneAdjustmentChecksum(adjustment.ZoneID, adjustment.VersionNumber, adjustment.EffectiveFrom, adjustment.MultiplierNumerator, adjustment.MultiplierDenominator) != adjustment.Checksum {
			return nil, fmt.Errorf("Hypervisor Zone price adjustment checksum mismatch")
		}
		numerator, denominator = adjustment.MultiplierNumerator, adjustment.MultiplierDenominator
	}
	vcpuQuantity, ok := checkedHourlyLimit(cpuCores)
	if !ok {
		return nil, fmt.Errorf("Hypervisor vCPU hourly quantity exceeds BIGINT")
	}
	memoryQuantity, ok := checkedHourlyLimit(memoryMIB)
	if !ok {
		return nil, fmt.Errorf("Hypervisor memory hourly quantity exceeds BIGINT")
	}
	diskQuantity, ok := checkedHourlyLimit(diskGIB)
	if !ok {
		return nil, fmt.Errorf("Hypervisor disk hourly quantity exceeds BIGINT")
	}
	vcpuCost, err := hypervisorComponentCharge(uint64(vcpuQuantity), vcpu.Brackets, numerator, denominator)
	if err != nil {
		return nil, err
	}
	memoryCost, err := hypervisorComponentCharge(uint64(memoryQuantity), memory.Brackets, numerator, denominator)
	if err != nil {
		return nil, err
	}
	diskCost, err := hypervisorComponentCharge(uint64(diskQuantity), disk.Brackets, numerator, denominator)
	if err != nil {
		return nil, err
	}
	hourly, ok := checkedAdd3(vcpuCost, memoryCost, diskCost)
	if !ok {
		return nil, fmt.Errorf("Hypervisor hourly estimate exceeds BIGINT")
	}
	monthly, ok := checkedMonthlyEstimate(hourly)
	if !ok {
		return nil, fmt.Errorf("Hypervisor monthly estimate exceeds BIGINT")
	}
	var adjustmentID *uuid.UUID
	var adjustmentVersion *int
	var adjustmentChecksum *string
	if adjustment != nil {
		adjustmentID = &adjustment.ID
		adjustmentVersion = &adjustment.VersionNumber
		adjustmentChecksum = &adjustment.Checksum
	}
	return &entity.HypervisorEstimate{
		CPUCores: cpuCores, MemoryMIB: memoryMIB, DiskGIB: diskGIB,
		VCPUHourlyMicroUnits: vcpuCost, MemoryHourlyMicroUnits: memoryCost,
		DiskHourlyMicroUnits: diskCost, HourlyMicroUnits: hourly,
		Monthly730HourMicroUnits: monthly, Currency: vcpu.Currency,
		VCPUScheduleCode: vcpu.Code, VCPUScheduleID: vcpu.PricingScheduleID,
		VCPUScheduleVersionID: vcpu.VersionID, VCPUVersion: vcpu.VersionNumber, VCPUChecksum: vcpu.Checksum,
		MemoryScheduleCode: memory.Code, MemoryScheduleID: memory.PricingScheduleID,
		MemoryScheduleVersionID: memory.VersionID, MemoryVersion: memory.VersionNumber, MemoryChecksum: memory.Checksum,
		DiskScheduleCode: disk.Code, DiskScheduleID: disk.PricingScheduleID,
		DiskScheduleVersionID: disk.VersionID, DiskVersion: disk.VersionNumber, DiskChecksum: disk.Checksum,
		RateAdjustmentID: adjustmentID, RateAdjustmentVersion: adjustmentVersion,
		RateAdjustmentChecksum: adjustmentChecksum, RateAdjustmentNumerator: numerator,
		RateAdjustmentDenominator: denominator, EstimatedAt: now,
	}, nil
}

func (s *hypervisorEstimateService) RunPricingCacheInvalidation(ctx context.Context) {
	s.pricingCache.runInvalidation(ctx)
}

func (s *hypervisorEstimateService) RunPricingReadinessProjection(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		now := time.Now().UTC()
		payload := s.hypervisorPricingReadiness(ctx, now)
		if encoded, err := json.Marshal(payload); err != nil {
			logger.SysError("billing.hypervisor.pricing_readiness.encode", err.Error())
		} else if err := s.pricingCache.redisClient.XAdd(ctx, &goredis.XAddArgs{
			Stream: hypervisorPricingReadinessStream,
			Values: map[string]any{"payload": encoded},
		}).Err(); err != nil && ctx.Err() == nil {
			logger.SysError("billing.hypervisor.pricing_readiness.publish", err.Error())
		} else if err := s.pricingCache.redisClient.Do(ctx, "WAITAOF", 1, 1, 500).Err(); err != nil && ctx.Err() == nil {
			logger.SysError("billing.hypervisor.pricing_readiness.durability", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *hypervisorEstimateService) hypervisorPricingReadiness(ctx context.Context, now time.Time) hypervisorPricingReadinessPayload {
	missing := make([]string, 0, 5)
	fingerprint := sha256.New()
	for _, chargeKind := range []entity.ChargeKindCode{
		entity.ChargeKindHypervisorVCPU,
		entity.ChargeKindHypervisorMemoryMIB,
		entity.ChargeKindHypervisorDiskGIB,
		entity.ChargeKindHypervisorNetworkIn,
		entity.ChargeKindHypervisorNetworkOut,
	} {
		snapshot, err := s.pricingCache.get(ctx, chargeKind, now)
		if err != nil || snapshot.ModuleCode != "hypervisor" {
			missing = append(missing, string(chargeKind))
			continue
		}
		_, _ = fingerprint.Write([]byte(string(chargeKind)))
		_, _ = fingerprint.Write(snapshot.VersionID[:])
		_, _ = fingerprint.Write([]byte(snapshot.Checksum))
	}
	return hypervisorPricingReadinessPayload{
		SchemaVersion: 1,
		Ready:         len(missing) == 0,
		Missing:       missing,
		ObservedAt:    now.Format(time.RFC3339Nano),
		ValidUntil:    now.Add(hypervisorPricingReadinessTTL).Format(time.RFC3339Nano),
		Fingerprint:   fmt.Sprintf("%x", fingerprint.Sum(nil)),
	}
}

func checkedHourlyLimit(limit int64) (int64, bool) {
	quantity := new(big.Int).Mul(big.NewInt(limit), big.NewInt(3_600))
	if !quantity.IsInt64() {
		return 0, false
	}
	return quantity.Int64(), true
}

func checkedAdd3(first, second, third int64) (int64, bool) {
	total := new(big.Int).Add(big.NewInt(first), big.NewInt(second))
	total.Add(total, big.NewInt(third))
	return total.Int64(), total.IsInt64()
}

func checkedMonthlyEstimate(hourly int64) (int64, bool) {
	total := new(big.Int).Mul(big.NewInt(hourly), big.NewInt(730))
	return total.Int64(), total.IsInt64()
}

func hypervisorComponentCharge(quantity uint64, brackets []entity.PricingSnapshotBracket, adjustmentNumerator, adjustmentDenominator int64) (int64, error) {
	if len(brackets) == 0 {
		return 0, billingTaxonomy.ErrInvalidPricingBrackets
	}
	total := new(big.Rat)
	for _, bracket := range brackets {
		if bracket.RangeStartQuantity < 0 || bracket.PriceNumeratorMicroUnits < 0 || bracket.PriceDenominatorQuantity <= 0 {
			return 0, billingTaxonomy.ErrInvalidPricingBrackets
		}
		start := uint64(bracket.RangeStartQuantity)
		if quantity <= start {
			break
		}
		upper := quantity
		if bracket.RangeEndQuantity != nil {
			if *bracket.RangeEndQuantity <= bracket.RangeStartQuantity {
				return 0, billingTaxonomy.ErrInvalidPricingBrackets
			}
			if uint64(*bracket.RangeEndQuantity) < upper {
				upper = uint64(*bracket.RangeEndQuantity)
			}
		}
		if upper > start {
			units := new(big.Int).SetUint64(upper - start)
			price := new(big.Int).Mul(units, big.NewInt(bracket.PriceNumeratorMicroUnits))
			total.Add(total, new(big.Rat).SetFrac(price, big.NewInt(bracket.PriceDenominatorQuantity)))
		}
	}
	if adjustmentNumerator < 0 || adjustmentDenominator <= 0 {
		return 0, billingTaxonomy.ErrInvalidArgument
	}
	total.Mul(total, new(big.Rat).SetFrac(big.NewInt(adjustmentNumerator), big.NewInt(adjustmentDenominator)))
	ceil := new(big.Int).Quo(total.Num(), total.Denom())
	if new(big.Int).Mod(total.Num(), total.Denom()).Sign() != 0 {
		ceil.Add(ceil, big.NewInt(1))
	}
	if !ceil.IsInt64() {
		return 0, fmt.Errorf("Hypervisor pricing charge exceeds BIGINT")
	}
	return ceil.Int64(), nil
}
