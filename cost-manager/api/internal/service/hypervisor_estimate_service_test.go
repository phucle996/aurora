package service

import (
	"context"
	"math"
	"testing"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingTaxonomy "cost-manager/api/internal/taxonomy"

	"github.com/google/uuid"
)

type hypervisorPricingReadinessRepoStub struct {
	snapshots map[entity.ChargeKindCode]*entity.PricingSnapshot
}

func (r *hypervisorPricingReadinessRepoStub) GetActivePricingSnapshot(_ context.Context, kind entity.ChargeKindCode, _ time.Time) (*entity.PricingSnapshot, error) {
	snapshot := r.snapshots[kind]
	if snapshot == nil {
		return nil, billingTaxonomy.ErrPricingScheduleNotFound
	}
	return snapshot, nil
}

func TestHypervisorEstimateUsesIntegerSecondsAndOneFinalCeiling(t *testing.T) {
	charge, err := hypervisorComponentCharge(
		3_600,
		[]entity.PricingSnapshotBracket{{
			RangeStartQuantity:       0,
			PriceNumeratorMicroUnits: 1,
			PriceDenominatorQuantity: 100,
		}},
		105,
		100,
	)
	if err != nil {
		t.Fatalf("charge component: %v", err)
	}
	if charge != 38 {
		t.Fatalf("expected ceil(3600/100*105/100)=38, got %d", charge)
	}
	quantity, ok := checkedHourlyLimit(64)
	if !ok || quantity != 64*3_600 {
		t.Fatalf("hourly limit must use exact integer seconds, got %d ok=%v", quantity, ok)
	}
}

func TestHypervisorEstimateRejectsBigintOverflow(t *testing.T) {
	if _, ok := checkedHourlyLimit(math.MaxInt64); ok {
		t.Fatal("hourly limit overflow must fail closed")
	}
	if _, ok := checkedMonthlyEstimate(math.MaxInt64); ok {
		t.Fatal("monthly estimate overflow must fail closed")
	}
}

func TestHypervisorPricingReadinessRequiresEveryBillableKind(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	repo := &hypervisorPricingReadinessRepoStub{snapshots: make(map[entity.ChargeKindCode]*entity.PricingSnapshot)}
	for _, kind := range []entity.ChargeKindCode{
		entity.ChargeKindHypervisorVCPU,
		entity.ChargeKindHypervisorMemoryMIB,
		entity.ChargeKindHypervisorDiskGIB,
		entity.ChargeKindHypervisorNetworkIn,
		entity.ChargeKindHypervisorNetworkOut,
	} {
		snapshot := &entity.PricingSnapshot{
			PricingScheduleID: uuid.New(),
			VersionID:         uuid.New(),
			Code:              "schedule-" + string(kind),
			ChargeKindCode:    kind,
			ModuleCode:        "hypervisor",
			PricingModel:      entity.PricingModelProgressiveUnit,
			RawInputUnit:      "BYTE",
			VersionNumber:     1,
			EffectiveFrom:     now.Add(-time.Hour),
			Currency:          "USD",
			Brackets: []entity.PricingSnapshotBracket{{
				ID:                       uuid.New(),
				RangeStartQuantity:       0,
				PriceNumeratorMicroUnits: 1,
				PriceDenominatorQuantity: 1,
			}},
		}
		snapshot.Checksum = pricingSnapshotChecksum(*snapshot)
		repo.snapshots[kind] = snapshot
	}
	service := NewHypervisorEstimateService(repo, nil, nil)
	payload := service.hypervisorPricingReadiness(context.Background(), now)
	if !payload.Ready || len(payload.Missing) != 0 || len(payload.Fingerprint) != 64 {
		t.Fatalf("unexpected readiness projection: %#v", payload)
	}
	delete(repo.snapshots, entity.ChargeKindHypervisorNetworkOut)
	notReady := NewHypervisorEstimateService(repo, nil, nil).hypervisorPricingReadiness(context.Background(), now)
	if notReady.Ready || len(notReady.Missing) != 1 || notReady.Missing[0] != string(entity.ChargeKindHypervisorNetworkOut) {
		t.Fatalf("missing network pricing did not fail readiness closed: %#v", notReady)
	}
}
