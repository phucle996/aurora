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

type revisionService struct {
	repo    managedrepo.RevisionRepository
	metrics observability.WorkflowRecorder
}

func NewRevisionService(repo managedrepo.RevisionRepository, metrics observability.WorkflowRecorder) managedservice.RevisionService {
	return &revisionService{repo: repo, metrics: metrics}
}
func (s *revisionService) CreateDraft(ctx context.Context, in *entity.CreateDraft) (out *entity.DraftView, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, taxonomy.ErrCatalogParentRetired), errors.Is(err, taxonomy.ErrCatalogRecordImmutable):
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
	return s.repo.CreateDraft(ctx, in)
}
func (s *revisionService) GetDraft(ctx context.Context, in *entity.GetDraft) (out *entity.DraftView, err error) {
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
	return s.repo.GetDraft(ctx, in)
}
func (s *revisionService) ListRevisions(ctx context.Context, in *entity.ListRevisions) (out []entity.DraftView, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.ListRevisions(ctx, in)
}
func (s *revisionService) PatchDraft(ctx context.Context, in *entity.PatchDraft) (out *entity.DraftView, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, taxonomy.ErrCatalogRecordImmutable):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		case errors.Is(err, taxonomy.ErrCatalogConcurrentChange), errors.Is(err, taxonomy.ErrCatalogRevisionStale):
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
	return s.repo.PatchDraft(ctx, in)
}
func (s *revisionService) ValidateDraft(ctx context.Context, in *entity.ValidateDraft) (out *entity.DraftView, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, taxonomy.ErrCatalogRecordImmutable):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		case errors.Is(err, taxonomy.ErrCatalogRevisionStale):
			result, reason = observability.ResultRejected, observability.ReasonConflict
		case errors.Is(err, taxonomy.ErrCatalogValidationFailed):
			result, reason = observability.ResultRejected, observability.ReasonInvalidArgument
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
	return s.repo.ValidateDraft(ctx, in)
}
func (s *revisionService) PublishDraft(ctx context.Context, in *entity.PublishDraft) (out *entity.DraftView, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, taxonomy.ErrCatalogRecordImmutable):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		case errors.Is(err, taxonomy.ErrCatalogRevisionStale):
			result, reason = observability.ResultRejected, observability.ReasonConflict
		case errors.Is(err, taxonomy.ErrCatalogValidationFailed):
			result, reason = observability.ResultRejected, observability.ReasonInvalidArgument
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
	return s.repo.PublishDraft(ctx, in)
}
func (s *revisionService) RetireRevision(ctx context.Context, in *entity.RetireRevision) (out *entity.DraftView, err error) {
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
	return s.repo.RetireRevision(ctx, in)
}
func (s *revisionService) DeleteDraft(ctx context.Context, in *entity.DeleteDraft) (err error) {
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
	return s.repo.DeleteDraft(ctx, in)
}
