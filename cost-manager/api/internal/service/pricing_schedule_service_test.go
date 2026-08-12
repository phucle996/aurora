package service

import (
	"testing"
	"time"

	"cost-manager/api/internal/domain/entity"
	"github.com/google/uuid"
)

func TestValidateScalarBracketsRequiresContiguousInfinity(t *testing.T) {
	end := int64(100)
	if err := validateScalarBrackets([]entity.ScalarBracketInput{
		{RangeStartQuantity: 0, RangeEndQuantity: &end, PriceNumeratorMicroUnits: 1, PriceDenominatorQuantity: 1},
		{RangeStartQuantity: 100, PriceNumeratorMicroUnits: 2, PriceDenominatorQuantity: 1},
	}); err != nil {
		t.Fatalf("expected contiguous brackets to validate: %v", err)
	}

	gap := int64(101)
	if err := validateScalarBrackets([]entity.ScalarBracketInput{
		{RangeStartQuantity: 0, RangeEndQuantity: &gap, PriceNumeratorMicroUnits: 1, PriceDenominatorQuantity: 1},
		{RangeStartQuantity: 100, PriceNumeratorMicroUnits: 2, PriceDenominatorQuantity: 1},
	}); err == nil {
		t.Fatal("expected a gap to be rejected")
	}
}

func TestChargeProgressiveQuantityRoundsOnceAfterSummingRationals(t *testing.T) {
	end := int64(2)
	charge, err := chargeProgressiveQuantity(3, []entity.ScalarBracketInput{
		{RangeStartQuantity: 0, RangeEndQuantity: &end, PriceNumeratorMicroUnits: 1, PriceDenominatorQuantity: 2},
		{RangeStartQuantity: 2, PriceNumeratorMicroUnits: 1, PriceDenominatorQuantity: 3},
	})
	if err != nil {
		t.Fatalf("charge failed: %v", err)
	}
	if charge != 2 {
		t.Fatalf("expected one final ceiling of 4/3 to equal 2, got %d", charge)
	}
}

func TestPricingScheduleChecksumChangesWithScopeAndVersion(t *testing.T) {
	schedule := entity.PricingSchedule{
		ID: uuid.New(), Code: "storage-capacity-payg", ChargeKindCode: entity.ChargeKindStorageCapacity,
		PricingModel: entity.PricingModelProgressiveUnit, ScopeType: entity.PricingScopeGlobal, Currency: "USD",
	}
	brackets := []entity.ScalarBracketInput{{RangeStartQuantity: 0, PriceNumeratorMicroUnits: 15, PriceDenominatorQuantity: 1}}
	effectiveFrom := time.Date(2026, 7, 18, 10, 11, 25, 589234000, time.UTC)
	first := pricingScheduleChecksum(schedule, 1, effectiveFrom, brackets)
	second := pricingScheduleChecksum(schedule, 2, effectiveFrom, brackets)
	zone := schedule
	zone.ScopeType = entity.PricingScopeZone
	zoneID := uuid.New()
	zone.ZoneID = &zoneID
	third := pricingScheduleChecksum(zone, 1, effectiveFrom, brackets)
	otherZone := zone
	otherZoneID := uuid.New()
	otherZone.ZoneID = &otherZoneID
	fourth := pricingScheduleChecksum(otherZone, 1, effectiveFrom, brackets)
	if first == second || first == third || third == fourth {
		t.Fatal("expected version, scope, and Zone to be part of the checksum")
	}
}
