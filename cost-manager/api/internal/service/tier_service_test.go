package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingTaxonomy "cost-manager/api/internal/taxonomy"

	"github.com/google/uuid"
)

type tierRepositoryStub struct {
	metadataFn func(context.Context, entity.TierMetadataUpdate) (*entity.TierMetadata, error)
	versionFn  func(context.Context, entity.TierVersionCreate) (*entity.TierVersion, error)
}

func (s *tierRepositoryStub) ListTiers(ctx context.Context, page, limit int, serviceType entity.ServiceType, search string) ([]*entity.Tier, int64, error) {
	return nil, 0, nil
}

func (s *tierRepositoryStub) GetTierDetail(ctx context.Context, code string, serviceType entity.ServiceType) (*entity.TierDetail, error) {
	return nil, billingTaxonomy.ErrTierNotFound
}

func (s *tierRepositoryStub) UpdateTierMetadata(ctx context.Context, update entity.TierMetadataUpdate) (*entity.TierMetadata, error) {
	return s.metadataFn(ctx, update)
}

func (s *tierRepositoryStub) CreateTierVersion(ctx context.Context, create entity.TierVersionCreate) (*entity.TierVersion, error) {
	return s.versionFn(ctx, create)
}

func TestTierServiceCreateVersionRejectsInvalidRanges(t *testing.T) {
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
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &tierRepositoryStub{
				metadataFn: func(context.Context, entity.TierMetadataUpdate) (*entity.TierMetadata, error) { return nil, nil },
				versionFn: func(context.Context, entity.TierVersionCreate) (*entity.TierVersion, error) {
					t.Fatal("repository was called for invalid ranges")
					return nil, nil
				},
			}
			svc := NewTierService(repo)
			_, err := svc.CreateTierVersion(context.Background(), validCreate(test.ranges))
			if !errors.Is(err, billingTaxonomy.ErrInvalidTierRanges) {
				t.Fatalf("expected ErrInvalidTierRanges, got %v", err)
			}
		})
	}
}

// [COMMENT]: TestTierServiceCreateVersionSortsAndComputesChecksum xác nhận logic gom nhóm và sort hoạt động đúng đắn.
func TestTierServiceCreateVersionSortsAndComputesChecksum(t *testing.T) {
	repo := &tierRepositoryStub{
		metadataFn: func(context.Context, entity.TierMetadataUpdate) (*entity.TierMetadata, error) { return nil, nil },
		versionFn: func(_ context.Context, create entity.TierVersionCreate) (*entity.TierVersion, error) {
			if create.Ranges[0].RangeStart != 0 || create.Ranges[1].RangeStart != 10 {
				t.Fatalf("ranges were not sorted: %+v", create.Ranges)
			}
			if len(create.Checksum) != 64 {
				t.Fatalf("expected SHA-256 checksum, got %q", create.Checksum)
			}
			return &entity.TierVersion{VersionNumber: create.ExpectedLatestVersion + 1, Checksum: create.Checksum}, nil
		},
	}
	svc := NewTierService(repo)
	create := validCreate([]entity.TierRangeInput{
		{RangeStart: 10, RangeEnd: 0, BaseUnitPrice: 12},
		{RangeStart: 0, RangeEnd: 10, BaseUnitPrice: 15},
	})
	result, err := svc.CreateTierVersion(context.Background(), create)
	if err != nil {
		t.Fatalf("CreateTierVersion returned error: %v", err)
	}
	if result.VersionNumber != 2 {
		t.Fatalf("unexpected version: %+v", result)
	}
}

func TestTierServiceMetadataUpdateDoesNotCreatePricingVersion(t *testing.T) {
	versionCalled := false
	repo := &tierRepositoryStub{
		metadataFn: func(_ context.Context, update entity.TierMetadataUpdate) (*entity.TierMetadata, error) {
			return &entity.TierMetadata{Name: update.Name, MetadataVersion: update.MetadataVersion + 1}, nil
		},
		versionFn: func(context.Context, entity.TierVersionCreate) (*entity.TierVersion, error) {
			versionCalled = true
			return nil, nil
		},
	}
	svc := NewTierService(repo)
	result, err := svc.UpdateTierMetadata(context.Background(), entity.TierMetadataUpdate{
		Code: "STORAGE_STD_BASE", ServiceType: entity.ServiceTypeStorage, MetadataVersion: 1, Name: "Renamed Storage",
	})
	if err != nil || result.MetadataVersion != 2 || versionCalled {
		t.Fatalf("metadata update leaked into pricing path: result=%+v err=%v versionCalled=%v", result, err, versionCalled)
	}
}

func TestTierVersionChecksumMatchesCrossLanguageContract(t *testing.T) {
	checksum := tierVersionChecksum(entity.TierVersionCreate{
		Code: "CODE", ServiceType: entity.ServiceTypeStorage,
		Ranges: []entity.TierRangeInput{
			{RangeStart: 0, RangeEnd: 10, BaseUnitPrice: 15},
			{RangeStart: 10, RangeEnd: 0, BaseUnitPrice: 12},
		},
	})
	const expected = "7159ff73182d252b26bdeae4757467a99d2776a229f5a154de029c5cb0c47099"
	if checksum != expected {
		t.Fatalf("checksum contract drifted: got %s", checksum)
	}
}

func validCreate(ranges []entity.TierRangeInput) entity.TierVersionCreate {
	return entity.TierVersionCreate{
		Code: "STORAGE_STD_BASE", ServiceType: entity.ServiceTypeStorage, ExpectedLatestVersion: 1,
		EffectiveFrom: time.Now().UTC().Add(time.Hour), ChangeReason: "scheduled test update",
		CreatedBy: uuid.New(), Ranges: ranges,
	}
}
