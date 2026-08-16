package service

import (
	"context"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"
)

type storageZoneAdjustmentListService struct {
	repo billingRepoInterface.StorageZoneAdjustmentListRepository
}

func NewStorageZoneAdjustmentListService(repo billingRepoInterface.StorageZoneAdjustmentListRepository) billingSvcInterface.StorageZoneAdjustmentListService {
	return &storageZoneAdjustmentListService{repo: repo}
}

func (s *storageZoneAdjustmentListService) ListStorageZonePriceAdjustments(ctx context.Context, query entity.StorageZoneAdjustmentListQuery) (*entity.StorageZoneAdjustmentListResult, error) {
	items, hasMore, err := s.repo.ListStorageZonePriceAdjustments(ctx, query)
	if err != nil {
		return nil, err
	}
	return &entity.StorageZoneAdjustmentListResult{
		ZoneID: query.ZoneID, Items: items, HasMore: hasMore, ObservedAt: time.Now().UTC(),
	}, nil
}
