package service

import (
	"context"
	"errors"
	"time"

	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	managedservice "controlplane/internal/managedservice/domain/service"
	"controlplane/internal/managedservice/taxonomy"
	"controlplane/internal/observability"
)

type personalCatalogService struct {
	repo    managedrepo.PersonalCatalogRepository
	metrics observability.WorkflowRecorder
}

func NewPersonalCatalogService(repo managedrepo.PersonalCatalogRepository, metrics observability.WorkflowRecorder) managedservice.PersonalCatalogService {
	return &personalCatalogService{repo: repo, metrics: metrics}
}

func (s *personalCatalogService) ListPersonalCatalog(ctx context.Context, in *entity.ListPersonalCatalog) (out *entity.PersonalCatalogPage, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, taxonomy.ErrCustomerCatalogUnavailable) {
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.ListPersonalCatalog(ctx, in)
}
