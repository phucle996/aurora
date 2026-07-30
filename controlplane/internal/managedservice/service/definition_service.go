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

type definitionService struct {
	repo    managedrepo.DefinitionRepository
	metrics observability.WorkflowRecorder
}

func NewDefinitionService(repo managedrepo.DefinitionRepository, metrics observability.WorkflowRecorder) managedservice.DefinitionService {
	return &definitionService{repo: repo, metrics: metrics}
}
func (s *definitionService) CreateDefinition(ctx context.Context, in *entity.CreateDefinition) (out *entity.DefinitionView, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, taxonomy.ErrCatalogCodeConflict):
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
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
	return s.repo.CreateDefinition(ctx, in)
}
func (s *definitionService) ListDefinitions(ctx context.Context, in *entity.ListDefinitions) (out []entity.DefinitionView, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.ListDefinitions(ctx, in)
}
func (s *definitionService) GetDefinition(ctx context.Context, in *entity.GetDefinition) (out *entity.DefinitionView, err error) {
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
	return s.repo.GetDefinition(ctx, in)
}
func (s *definitionService) UpdateDefinition(ctx context.Context, in *entity.UpdateDefinition) (out *entity.DefinitionView, err error) {
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
	return s.repo.UpdateDefinition(ctx, in)
}
func (s *definitionService) RetireDefinition(ctx context.Context, in *entity.RetireDefinition) (out *entity.DefinitionView, err error) {
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
	return s.repo.RetireDefinition(ctx, in)
}
