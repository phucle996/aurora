package service

import (
	"context"
	"errors"
	"testing"

	"cost-manager/api/internal/domain/entity"
	billingTaxonomy "cost-manager/api/internal/taxonomy"

	"github.com/google/uuid"
)

// tierRepositoryStub giữ unit test tập trung vào domain validation, không cần PostgreSQL thật.
type tierRepositoryStub struct {
	updateFn func(context.Context, entity.TierUpdate) (*entity.TierAggregate, error)
}

func (s *tierRepositoryStub) ListTiers(context.Context, int, int, string, string) ([]*entity.Tier, int64, error) {
	return nil, 0, nil
}

func (s *tierRepositoryStub) UpdateTier(ctx context.Context, update entity.TierUpdate) (*entity.TierAggregate, error) {
	return s.updateFn(ctx, update)
}

func TestTierServiceUpdateTierRejectsInvalidRanges(t *testing.T) {
	sharedID := uuid.New()
	tests := []struct {
		name   string
		ranges []entity.TierRangeInput
	}{
		{name: "does not start at zero", ranges: []entity.TierRangeInput{{RangeStart: 1, RangeEnd: 0}}},
		{name: "contains gap", ranges: []entity.TierRangeInput{{RangeStart: 0, RangeEnd: 10}, {RangeStart: 11, RangeEnd: 0}}},
		{name: "contains overlap", ranges: []entity.TierRangeInput{{RangeStart: 0, RangeEnd: 10}, {RangeStart: 9, RangeEnd: 0}}},
		{name: "infinity is not last", ranges: []entity.TierRangeInput{{RangeStart: 0, RangeEnd: 0}, {RangeStart: 10, RangeEnd: 0}}},
		{name: "last range is finite", ranges: []entity.TierRangeInput{{RangeStart: 0, RangeEnd: 10}}},
		{name: "negative price", ranges: []entity.TierRangeInput{{RangeStart: 0, RangeEnd: 0, BaseUnitPrice: -1}}},
		{name: "duplicate existing id", ranges: []entity.TierRangeInput{{ID: sharedID, RangeStart: 0, RangeEnd: 10}, {ID: sharedID, RangeStart: 10, RangeEnd: 0}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Repository không được gọi khi aggregate đã sai ở domain layer.
			repo := &tierRepositoryStub{updateFn: func(context.Context, entity.TierUpdate) (*entity.TierAggregate, error) {
				t.Fatal("repository was called for invalid ranges")
				return nil, nil
			}}
			svc := NewTierService(repo)

			_, err := svc.UpdateTier(context.Background(), entity.TierUpdate{
				Code: "STORAGE_STD_BASE", ServiceType: "STORAGE", Version: 1, Name: "Storage", Ranges: test.ranges,
			})
			if !errors.Is(err, billingTaxonomy.ErrInvalidTierRanges) {
				t.Fatalf("expected ErrInvalidTierRanges, got %v", err)
			}
		})
	}
}

func TestTierServiceUpdateTierSortsAndDelegatesValidAggregate(t *testing.T) {
	repo := &tierRepositoryStub{updateFn: func(_ context.Context, update entity.TierUpdate) (*entity.TierAggregate, error) {
		// Service phải canonicalize ranges theo boundary trước khi repository reconcile.
		if update.Ranges[0].RangeStart != 0 || update.Ranges[1].RangeStart != 10 {
			t.Fatalf("ranges were not sorted: %+v", update.Ranges)
		}
		return &entity.TierAggregate{Code: update.Code, Version: update.Version + 1}, nil
	}}
	svc := NewTierService(repo)

	result, err := svc.UpdateTier(context.Background(), entity.TierUpdate{
		Code: " STORAGE_STD_BASE ", ServiceType: " STORAGE ", Version: 2, Name: " Storage ",
		Ranges: []entity.TierRangeInput{{RangeStart: 10, RangeEnd: 0, BaseUnitPrice: 12}, {RangeStart: 0, RangeEnd: 10, BaseUnitPrice: 15}},
	})
	if err != nil {
		t.Fatalf("UpdateTier returned error: %v", err)
	}
	if result.Version != 3 || result.Code != "STORAGE_STD_BASE" {
		t.Fatalf("unexpected aggregate: %+v", result)
	}
}
