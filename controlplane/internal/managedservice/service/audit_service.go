package service

import (
	"context"
	"time"

	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	managedservice "controlplane/internal/managedservice/domain/service"
	"controlplane/internal/observability"
)

type auditService struct {
	repo    managedrepo.AuditRepository
	metrics observability.WorkflowRecorder
}

func NewAuditService(repo managedrepo.AuditRepository, metrics observability.WorkflowRecorder) managedservice.AuditService {
	return &auditService{repo: repo, metrics: metrics}
}
func (s *auditService) ListAuditEvents(ctx context.Context, in *entity.ListAuditEvents) (out []entity.AuditEventView, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.ListAuditEvents(ctx, in)
}
