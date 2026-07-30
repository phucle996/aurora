package service

import (
	"context"
	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	managedservice "controlplane/internal/managedservice/domain/service"
)

type auditService struct{ repo managedrepo.AuditRepository }

func NewAuditService(repo managedrepo.AuditRepository) managedservice.AuditService {
	return &auditService{repo: repo}
}
func (s *auditService) ListAuditEvents(ctx context.Context, in *entity.ListAuditEvents) ([]entity.AuditEventView, error) {
	return s.repo.ListAuditEvents(ctx, in)
}
