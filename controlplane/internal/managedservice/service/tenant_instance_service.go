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

type tenantInstanceService struct {
	repo    managedrepo.TenantInstanceRepository
	metrics observability.WorkflowRecorder
}

func NewTenantInstanceService(repo managedrepo.TenantInstanceRepository, metrics observability.WorkflowRecorder) managedservice.TenantInstanceService {
	return &tenantInstanceService{repo: repo, metrics: metrics}
}

func (s *tenantInstanceService) ListTenantInstances(ctx context.Context, in *entity.ListTenantInstances) (out *entity.TenantInstancePage, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, taxonomy.ErrUnavailable) {
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.ListTenantInstances(ctx, in)
}

func (s *tenantInstanceService) GetTenantInstance(ctx context.Context, in *entity.GetTenantInstance) (out *entity.TenantInstanceDetail, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, taxonomy.ErrNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, taxonomy.ErrUnavailable):
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.GetTenantInstance(ctx, in)
}

func (s *tenantInstanceService) ListTenantInstanceOperations(ctx context.Context, in *entity.ListTenantInstanceOperations) (out *entity.TenantInstanceOperationPage, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, taxonomy.ErrUnavailable) {
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.ListTenantInstanceOperations(ctx, in)
}

func (s *tenantInstanceService) GetTenantInstanceOperation(ctx context.Context, in *entity.GetTenantInstanceOperation) (out *entity.TenantInstanceOperationDetail, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, taxonomy.ErrNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, taxonomy.ErrUnavailable):
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.GetTenantInstanceOperation(ctx, in)
}

func (s *tenantInstanceService) RenameTenantInstance(ctx context.Context, in *entity.RenameTenantInstance) (out *entity.RenameTenantInstanceResult, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, taxonomy.ErrNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, taxonomy.ErrConflict):
			result, reason = observability.ResultRejected, observability.ReasonConflict
		case errors.Is(err, taxonomy.ErrUnavailable):
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.RenameTenantInstance(ctx, in)
}
