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

type tenantInstanceService struct {
	repo    managedrepo.TenantInstanceRepository
	metrics observability.WorkflowRecorder
}

func NewTenantInstanceService(repo managedrepo.TenantInstanceRepository, metrics observability.WorkflowRecorder) managedservice.TenantInstanceService {
	return &tenantInstanceService{repo: repo, metrics: metrics}
}

func (s *tenantInstanceService) CreateTenantInstance(ctx context.Context, in *entity.CreateTenantInstance) (out *entity.CreateTenantInstanceResult, err error) {
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
	return s.repo.CreateTenantInstance(ctx, in)
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

func (s *tenantInstanceService) ResizeTenantInstance(ctx context.Context, in *entity.ResizeTenantInstance) (out *entity.ResizeTenantInstanceResult, err error) {
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
	return s.repo.ResizeTenantInstance(ctx, in)
}

func (s *tenantInstanceService) DeleteTenantInstance(ctx context.Context, in *entity.DeleteTenantInstance) (out *entity.DeleteTenantInstanceResult, err error) {
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
	return s.repo.DeleteTenantInstance(ctx, in)
}

func (s *tenantInstanceService) RetryTenantInstance(ctx context.Context, in *entity.RetryTenantInstance) (out *entity.RetryTenantInstanceResult, err error) {
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
	return s.repo.RetryTenantInstance(ctx, in)
}
