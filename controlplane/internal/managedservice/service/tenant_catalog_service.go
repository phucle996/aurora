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

type tenantCatalogService struct {
	repo    managedrepo.TenantCatalogRepository
	metrics observability.WorkflowRecorder
}

func NewTenantCatalogService(repo managedrepo.TenantCatalogRepository, metrics observability.WorkflowRecorder) managedservice.TenantCatalogService {
	return &tenantCatalogService{repo: repo, metrics: metrics}
}

func (s *tenantCatalogService) ListTenantCatalog(ctx context.Context, in *entity.ListTenantCatalog) (out *entity.TenantCatalogPage, err error) {
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
	return s.repo.ListTenantCatalog(ctx, in)
}
