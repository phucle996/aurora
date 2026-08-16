package service

import (
	"context"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"
)

type mailZoneAdjustmentListService struct {
	repo billingRepoInterface.MailZoneAdjustmentListRepository
}

func NewMailZoneAdjustmentListService(repo billingRepoInterface.MailZoneAdjustmentListRepository) billingSvcInterface.MailZoneAdjustmentListService {
	return &mailZoneAdjustmentListService{repo: repo}
}

func (s *mailZoneAdjustmentListService) ListMailZonePriceAdjustments(ctx context.Context, query entity.MailZoneAdjustmentListQuery) (*entity.MailZoneAdjustmentListResult, error) {
	items, hasMore, err := s.repo.ListMailZonePriceAdjustments(ctx, query)
	if err != nil {
		return nil, err
	}
	return &entity.MailZoneAdjustmentListResult{ZoneID: query.ZoneID, Items: items, HasMore: hasMore, ObservedAt: time.Now().UTC()}, nil
}
