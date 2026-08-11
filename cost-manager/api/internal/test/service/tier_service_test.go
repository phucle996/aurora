package service_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingService "cost-manager/api/internal/service"

	"github.com/google/uuid"
)

type tierRepoStub struct {
	calls    atomic.Int32
	snapshot *entity.PricingSnapshot
}

func (stub *tierRepoStub) ListTiers(context.Context, int, int, entity.ServiceType, string) ([]*entity.Tier, int64, error) {
	return nil, 0, nil
}

func (stub *tierRepoStub) GetTierDetail(context.Context, string, entity.ServiceType) (*entity.TierDetail, error) {
	return nil, nil
}

func (stub *tierRepoStub) GetActivePricingSnapshot(context.Context, entity.ServiceType) (*entity.PricingSnapshot, error) {
	stub.calls.Add(1)
	return stub.snapshot, nil
}

func (stub *tierRepoStub) UpdateTierMetadata(context.Context, entity.TierMetadataUpdate) (*entity.TierMetadata, error) {
	return nil, nil
}

func (stub *tierRepoStub) CreateTierVersion(context.Context, entity.TierVersionCreate) (*entity.TierVersion, error) {
	return nil, nil
}

func TestEstimateStorageUsesActivePricingAndL1Cache(t *testing.T) {
	repo := &tierRepoStub{snapshot: &entity.PricingSnapshot{
		TierID: uuid.New(), TierVersionID: uuid.New(), Code: "STORAGE_STD_BASE",
		ServiceType: entity.ServiceTypeStorage, VersionNumber: 1,
		EffectiveFrom: time.Now().Add(-time.Minute), Currency: "USD",
		Ranges: []entity.TierRangeInput{
			{RangeStart: 0, RangeEnd: 50, BaseUnitPrice: 15_000},
			{RangeStart: 50, RangeEnd: 0, BaseUnitPrice: 12_000},
		},
	}}
	service := billingService.NewTierService(repo, nil)
	capacityBytes := int64(51_201_000_000)

	first, err := service.EstimateStorage(context.Background(), capacityBytes)
	if err != nil {
		t.Fatalf("first estimate: %v", err)
	}
	second, err := service.EstimateStorage(context.Background(), capacityBytes)
	if err != nil {
		t.Fatalf("second estimate: %v", err)
	}
	if first.HourlyMicroUnits != 764_412 || first.MonthlyMicroUnits != 558_020_760 {
		t.Fatalf("estimate = %#v", first)
	}
	if second.TierVersionID != first.TierVersionID || repo.calls.Load() != 1 {
		t.Fatalf("cache result/version or repository calls invalid: second=%#v calls=%d", second, repo.calls.Load())
	}
}

func TestEstimateStorageHandlesLargeExactIntegerCharge(t *testing.T) {
	repo := &tierRepoStub{snapshot: &entity.PricingSnapshot{
		TierID: uuid.New(), TierVersionID: uuid.New(), Code: "STORAGE_STD_BASE",
		ServiceType: entity.ServiceTypeStorage, VersionNumber: 1,
		EffectiveFrom: time.Now().Add(-time.Minute), Currency: "USD",
		Ranges: []entity.TierRangeInput{{RangeStart: 0, RangeEnd: 0, BaseUnitPrice: 1_000_000}},
	}}
	service := billingService.NewTierService(repo, nil)

	estimate, err := service.EstimateStorage(context.Background(), 1<<50)
	if err != nil {
		t.Fatalf("large estimate: %v", err)
	}
	if estimate.HourlyMicroUnits != 1_125_899_906_842 {
		t.Fatalf("hourly estimate = %d", estimate.HourlyMicroUnits)
	}
}
