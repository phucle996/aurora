package service

import (
	"testing"
	"time"

	"cost-manager/api/internal/domain/entity"
	"github.com/google/uuid"
)

func TestValidateScalarBracketsRequiresContiguousInfinity(t *testing.T) {
	end := int64(100)
	if err := validatePricingScheduleVersionPublishBrackets([]entity.PricingScheduleVersionPublishBracket{
		{RangeStartQuantity: 0, RangeEndQuantity: &end, PriceNumeratorMicroUnits: 1, PriceDenominatorQuantity: 1},
		{RangeStartQuantity: 100, PriceNumeratorMicroUnits: 2, PriceDenominatorQuantity: 1},
	}); err != nil {
		t.Fatalf("expected contiguous brackets to validate: %v", err)
	}

	gap := int64(101)
	if err := validatePricingScheduleVersionPublishBrackets([]entity.PricingScheduleVersionPublishBracket{
		{RangeStartQuantity: 0, RangeEndQuantity: &gap, PriceNumeratorMicroUnits: 1, PriceDenominatorQuantity: 1},
		{RangeStartQuantity: 100, PriceNumeratorMicroUnits: 2, PriceDenominatorQuantity: 1},
	}); err == nil {
		t.Fatal("expected a gap to be rejected")
	}
}

func TestChargeProgressiveQuantityRoundsOnceAfterSummingRationals(t *testing.T) {
	end := int64(2)
	charge, err := storageEstimateCharge(3, []entity.PricingSnapshotBracket{
		{RangeStartQuantity: 0, RangeEndQuantity: &end, PriceNumeratorMicroUnits: 1, PriceDenominatorQuantity: 2},
		{RangeStartQuantity: 2, PriceNumeratorMicroUnits: 1, PriceDenominatorQuantity: 3},
	}, 1, 1)
	if err != nil {
		t.Fatalf("charge failed: %v", err)
	}
	if charge != 2 {
		t.Fatalf("expected one final ceiling of 4/3 to equal 2, got %d", charge)
	}
}

func TestPricingScheduleChecksumChangesWithVersion(t *testing.T) {
	target := entity.PricingScheduleVersionPublishTarget{
		ScheduleCode: "storage-capacity-payg", ChargeKindCode: entity.ChargeKindStorageCapacity,
		PricingModel: entity.PricingModelProgressiveUnit, Currency: "USD",
	}
	brackets := []entity.PricingScheduleVersionPublishBracket{{RangeStartQuantity: 0, PriceNumeratorMicroUnits: 15, PriceDenominatorQuantity: 1}}
	effectiveFrom := time.Date(2026, 7, 18, 10, 11, 25, 589234000, time.UTC)
	first := pricingSchedulePublishChecksum(target, 1, effectiveFrom, brackets)
	second := pricingSchedulePublishChecksum(target, 2, effectiveFrom, brackets)
	if first == second {
		t.Fatal("expected version to be part of the checksum")
	}
}

func TestPricingScheduleChecksumMatchesCrossRuntimeGolden(t *testing.T) {
	end := int64(50_000_000_000)
	target := entity.PricingScheduleVersionPublishTarget{
		ScheduleCode:   "storage-capacity-payg",
		ChargeKindCode: entity.ChargeKindStorageCapacity,
		PricingModel:   entity.PricingModelProgressiveUnit,
		Currency:       "USD",
	}
	brackets := []entity.PricingScheduleVersionPublishBracket{
		{RangeStartQuantity: 0, RangeEndQuantity: &end, PriceNumeratorMicroUnits: 15_000, PriceDenominatorQuantity: 1_000_000_000},
		{RangeStartQuantity: end, PriceNumeratorMicroUnits: 12_000, PriceDenominatorQuantity: 1_000_000_000},
	}
	// The final 987 nanoseconds cannot survive PostgreSQL. The checksum must use
	// the same durable microsecond timestamp as the Rust Engine and seed SQL.
	effectiveFrom := time.Date(2026, 7, 18, 10, 11, 25, 589_234_987, time.UTC)
	const expected = "625d0d50cce646f5fbd2f988226d69f9d50f55c99cee9262990e060ba3f702d9"
	if actual := pricingSchedulePublishChecksum(target, 1, effectiveFrom, brackets); actual != expected {
		t.Fatalf("cross-runtime pricing checksum mismatch: got %s want %s", actual, expected)
	}
}

func TestChargeProgressiveQuantityAppliesAdjustmentBeforeCeiling(t *testing.T) {
	charge, err := storageEstimateCharge(1, []entity.PricingSnapshotBracket{{RangeStartQuantity: 0, PriceNumeratorMicroUnits: 1, PriceDenominatorQuantity: 2}}, 1, 2)
	if err != nil {
		t.Fatalf("charge failed: %v", err)
	}
	if charge != 1 {
		t.Fatalf("expected exact 1/4 to ceil once to 1, got %d", charge)
	}
}

func TestStorageByteHourChargeDoesNotDropSubKilobyteUsage(t *testing.T) {
	charge, err := storageEstimateCharge(1, []entity.PricingSnapshotBracket{{RangeStartQuantity: 0, PriceNumeratorMicroUnits: 15_000, PriceDenominatorQuantity: 1_000_000_000}}, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if charge != 1 {
		t.Fatalf("one occupied byte-hour must reach the money boundary: got %d", charge)
	}
}

func TestStorageZoneAdjustmentChecksumMatchesEngineGolden(t *testing.T) {
	zoneID := uuid.MustParse("019f3d3e-998a-7894-9236-c5122634cb5a")
	effectiveFrom := time.Date(2026, 8, 15, 13, 30, 0, 999, time.FixedZone("UTC+7", 7*60*60))
	const expected = "ff35883f8f350cb70e85beda5f9b29e9cdd0c9179b9186fd20223508c040d580"
	if actual := storageZoneAdjustmentChecksum(zoneID, 1, effectiveFrom, 105, 100); actual != expected {
		t.Fatalf("cross-runtime adjustment checksum mismatch: got %s want %s", actual, expected)
	}
}
