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

	"github.com/google/uuid"
)

type categoryService struct {
	repo    managedrepo.CategoryRepository
	metrics observability.WorkflowRecorder
}

func NewCategoryService(repo managedrepo.CategoryRepository, metrics observability.WorkflowRecorder) managedservice.CategoryService {
	return &categoryService{repo: repo, metrics: metrics}
}
func (s *categoryService) CreateCategory(ctx context.Context, in *entity.CreateCategory) (out *entity.CategoryView, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, taxonomy.ErrCatalogCodeConflict):
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		case errors.Is(err, taxonomy.ErrCatalogParentRetired):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	// [COMMENT]: The service owns system identities. Preserve populated values so an internal retry keeps the same resource and audit identities.
	if in.ID == uuid.Nil {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		in.ID = id
	}
	if in.AuditID == uuid.Nil {
		auditID, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		in.AuditID = auditID
	}
	return s.repo.CreateCategory(ctx, in)
}
func (s *categoryService) ListCategories(ctx context.Context, in *entity.ListCategories) (out []entity.CategoryView, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.ListCategories(ctx, in)
}
func (s *categoryService) GetCategory(ctx context.Context, in *entity.GetCategory) (out *entity.CategoryView, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, taxonomy.ErrCatalogNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.GetCategory(ctx, in)
}
func (s *categoryService) UpdateCategory(ctx context.Context, in *entity.UpdateCategory) (out *entity.CategoryView, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, taxonomy.ErrCatalogInvalidTransition):
			result, reason = observability.ResultRejected, observability.ReasonInvalidTransition
		case errors.Is(err, taxonomy.ErrCatalogConcurrentChange):
			result, reason = observability.ResultRejected, observability.ReasonConflict
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	if in.AuditID == uuid.Nil {
		auditID, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		in.AuditID = auditID
	}
	return s.repo.UpdateCategory(ctx, in)
}
func (s *categoryService) RetireCategory(ctx context.Context, in *entity.RetireCategory) (out *entity.CategoryView, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, taxonomy.ErrCatalogInvalidTransition):
			result, reason = observability.ResultRejected, observability.ReasonInvalidTransition
		case errors.Is(err, taxonomy.ErrCatalogConcurrentChange):
			result, reason = observability.ResultRejected, observability.ReasonConflict
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	if in.AuditID == uuid.Nil {
		auditID, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		in.AuditID = auditID
	}
	return s.repo.RetireCategory(ctx, in)
}
