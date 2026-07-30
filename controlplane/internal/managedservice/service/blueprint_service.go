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

type blueprintService struct {
	repo    managedrepo.BlueprintRepository
	metrics observability.WorkflowRecorder
}

func NewBlueprintService(repo managedrepo.BlueprintRepository, metrics observability.WorkflowRecorder) managedservice.BlueprintService {
	return &blueprintService{repo: repo, metrics: metrics}
}
func (s *blueprintService) CreateBlueprint(ctx context.Context, in *entity.CreateBlueprint) (out *entity.BlueprintView, err error) {
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
	return s.repo.CreateBlueprint(ctx, in)
}
func (s *blueprintService) GetBlueprint(ctx context.Context, in *entity.GetBlueprint) (out *entity.BlueprintView, err error) {
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
	return s.repo.GetBlueprint(ctx, in)
}
func (s *blueprintService) GetBlueprintByVersion(ctx context.Context, in *entity.GetBlueprintByVersion) (out *entity.BlueprintView, err error) {
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
	return s.repo.GetBlueprintByVersion(ctx, in)
}
func (s *blueprintService) DeleteBlueprint(ctx context.Context, in *entity.DeleteBlueprint) (err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, taxonomy.ErrCatalogRecordPinned), errors.Is(err, taxonomy.ErrCatalogRecordImmutable):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		case errors.Is(err, taxonomy.ErrCatalogConcurrentChange):
			result, reason = observability.ResultRejected, observability.ReasonConflict
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	if in.AuditID == uuid.Nil {
		auditID, err := uuid.NewV7()
		if err != nil {
			return err
		}
		in.AuditID = auditID
	}
	return s.repo.DeleteBlueprint(ctx, in)
}
