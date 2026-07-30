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

type personalCatalogVersionService struct {
	repo    managedrepo.PersonalCatalogVersionRepository
	metrics observability.WorkflowRecorder
}

func NewPersonalCatalogVersionService(repo managedrepo.PersonalCatalogVersionRepository, metrics observability.WorkflowRecorder) managedservice.PersonalCatalogVersionService {
	return &personalCatalogVersionService{repo: repo, metrics: metrics}
}

func (s *personalCatalogVersionService) GetPersonalCatalogVersion(ctx context.Context, in *entity.GetPersonalCatalogVersion) (out *entity.PersonalCatalogVersionView, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, taxonomy.ErrCustomerCatalogNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, taxonomy.ErrCustomerCatalogStale):
			result, reason = observability.ResultRejected, observability.ReasonConflict
		case errors.Is(err, taxonomy.ErrCustomerCatalogUnavailable):
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.GetPersonalCatalogVersion(ctx, in)
}
