package service

import (
	"context"
	"strings"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
)

type pricingScheduleRateStateService struct {
	repo billingRepoInterface.PricingScheduleRateStateRepository
}

func NewPricingScheduleRateStateService(repo billingRepoInterface.PricingScheduleRateStateRepository) billingSvcInterface.PricingScheduleRateStateService {
	return &pricingScheduleRateStateService{repo: repo}
}

func (s *pricingScheduleRateStateService) GetPricingScheduleRateState(ctx context.Context, code string) ([]entity.PricingScheduleRateStateRow, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	return s.repo.GetPricingScheduleRateState(ctx, code)
}
