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
	"go.opentelemetry.io/otel/trace"
)

type personalInstanceService struct {
	repo    managedrepo.PersonalInstanceRepository
	metrics observability.WorkflowRecorder
}

func NewPersonalInstanceService(repo managedrepo.PersonalInstanceRepository, metrics observability.WorkflowRecorder) managedservice.PersonalInstanceService {
	return &personalInstanceService{repo: repo, metrics: metrics}
}

func (s *personalInstanceService) CreatePersonalInstance(ctx context.Context, in *entity.CreatePersonalInstance) (out *entity.CreatePersonalInstanceResult, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, taxonomy.ErrConflict):
			result, reason = observability.ResultRejected, observability.ReasonConflict
		case errors.Is(err, taxonomy.ErrNotFound), errors.Is(err, taxonomy.ErrPreconditionFailed):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, taxonomy.ErrUnavailable):
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	if in.InstanceID == uuid.Nil {
		in.InstanceID, err = uuid.NewV7()
		if err != nil {
			return nil, err
		}
	}
	if in.InstanceRevisionID == uuid.Nil {
		in.InstanceRevisionID, err = uuid.NewV7()
		if err != nil {
			return nil, err
		}
	}
	if in.OperationID == uuid.Nil {
		in.OperationID, err = uuid.NewV7()
		if err != nil {
			return nil, err
		}
	}
	if in.CommandEventID == uuid.Nil {
		in.CommandEventID, err = uuid.NewV7()
		if err != nil {
			return nil, err
		}
	}
	if in.IssuedAt.IsZero() {
		in.IssuedAt = time.Now().UTC()
	}
	if len(in.TraceID) == 0 {
		if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
			traceID := spanContext.TraceID()
			in.TraceID = append([]byte(nil), traceID[:]...)
		}
	}
	return s.repo.CreatePersonalInstance(ctx, in)
}

func (s *personalInstanceService) ListPersonalInstances(ctx context.Context, in *entity.ListPersonalInstances) (out *entity.PersonalInstancePage, err error) {
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
	return s.repo.ListPersonalInstances(ctx, in)
}

func (s *personalInstanceService) GetPersonalInstance(ctx context.Context, in *entity.GetPersonalInstance) (out *entity.PersonalInstanceDetail, err error) {
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
	return s.repo.GetPersonalInstance(ctx, in)
}

func (s *personalInstanceService) ListPersonalInstanceOperations(ctx context.Context, in *entity.ListPersonalInstanceOperations) (out *entity.PersonalInstanceOperationPage, err error) {
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
	return s.repo.ListPersonalInstanceOperations(ctx, in)
}

func (s *personalInstanceService) GetPersonalInstanceOperation(ctx context.Context, in *entity.GetPersonalInstanceOperation) (out *entity.PersonalInstanceOperationDetail, err error) {
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
	return s.repo.GetPersonalInstanceOperation(ctx, in)
}

func (s *personalInstanceService) RenamePersonalInstance(ctx context.Context, in *entity.RenamePersonalInstance) (out *entity.RenamePersonalInstanceResult, err error) {
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
	return s.repo.RenamePersonalInstance(ctx, in)
}

func (s *personalInstanceService) ResizePersonalInstance(ctx context.Context, in *entity.ResizePersonalInstance) (out *entity.ResizePersonalInstanceResult, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, taxonomy.ErrNotFound), errors.Is(err, taxonomy.ErrPreconditionFailed):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, taxonomy.ErrConflict):
			result, reason = observability.ResultRejected, observability.ReasonConflict
		case errors.Is(err, taxonomy.ErrUnavailable):
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	if in.InstanceRevisionID == uuid.Nil {
		in.InstanceRevisionID, err = uuid.NewV7()
		if err != nil {
			return nil, err
		}
	}
	if in.OperationID == uuid.Nil {
		in.OperationID, err = uuid.NewV7()
		if err != nil {
			return nil, err
		}
	}
	if in.CommandEventID == uuid.Nil {
		in.CommandEventID, err = uuid.NewV7()
		if err != nil {
			return nil, err
		}
	}
	if in.IssuedAt.IsZero() {
		in.IssuedAt = time.Now().UTC()
	}
	if len(in.TraceID) == 0 {
		if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
			traceID := spanContext.TraceID()
			in.TraceID = append([]byte(nil), traceID[:]...)
		}
	}
	return s.repo.ResizePersonalInstance(ctx, in)
}

func (s *personalInstanceService) DeletePersonalInstance(ctx context.Context, in *entity.DeletePersonalInstance) (out *entity.DeletePersonalInstanceResult, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, taxonomy.ErrNotFound), errors.Is(err, taxonomy.ErrPreconditionFailed):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, taxonomy.ErrConflict):
			result, reason = observability.ResultRejected, observability.ReasonConflict
		case errors.Is(err, taxonomy.ErrUnavailable):
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	if in.OperationID == uuid.Nil {
		in.OperationID, err = uuid.NewV7()
		if err != nil {
			return nil, err
		}
	}
	if in.CommandEventID == uuid.Nil {
		in.CommandEventID, err = uuid.NewV7()
		if err != nil {
			return nil, err
		}
	}
	if in.IssuedAt.IsZero() {
		in.IssuedAt = time.Now().UTC()
	}
	if len(in.TraceID) == 0 {
		if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
			traceID := spanContext.TraceID()
			in.TraceID = append([]byte(nil), traceID[:]...)
		}
	}
	return s.repo.DeletePersonalInstance(ctx, in)
}

func (s *personalInstanceService) RetryPersonalInstance(ctx context.Context, in *entity.RetryPersonalInstance) (out *entity.RetryPersonalInstanceResult, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, taxonomy.ErrNotFound), errors.Is(err, taxonomy.ErrPreconditionFailed):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, taxonomy.ErrConflict):
			result, reason = observability.ResultRejected, observability.ReasonConflict
		case errors.Is(err, taxonomy.ErrUnavailable):
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.RetryPersonalInstance(ctx, in)
}
