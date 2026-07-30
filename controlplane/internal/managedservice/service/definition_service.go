package service

import (
	"context"
	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	managedservice "controlplane/internal/managedservice/domain/service"

	"github.com/google/uuid"
)

type definitionService struct {
	repo managedrepo.DefinitionRepository
}

func NewDefinitionService(repo managedrepo.DefinitionRepository) managedservice.DefinitionService {
	return &definitionService{repo: repo}
}
func (s *definitionService) CreateDefinition(ctx context.Context, in *entity.CreateDefinition) (*entity.DefinitionView, error) {
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
func (s *definitionService) ListDefinitions(ctx context.Context, in *entity.ListDefinitions) ([]entity.DefinitionView, error) {
	return s.repo.ListDefinitions(ctx, in)
}
func (s *definitionService) GetDefinition(ctx context.Context, in *entity.GetDefinition) (*entity.DefinitionView, error) {
	return s.repo.GetDefinition(ctx, in)
}
func (s *definitionService) UpdateDefinition(ctx context.Context, in *entity.UpdateDefinition) (*entity.DefinitionView, error) {
	if in.AuditID == uuid.Nil {
		auditID, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		in.AuditID = auditID
	}
	return s.repo.UpdateDefinition(ctx, in)
}
func (s *definitionService) RetireDefinition(ctx context.Context, in *entity.RetireDefinition) (*entity.DefinitionView, error) {
	if in.AuditID == uuid.Nil {
		auditID, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		in.AuditID = auditID
	}
	return s.repo.RetireDefinition(ctx, in)
}
