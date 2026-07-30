package service

import (
	"context"
	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	managedservice "controlplane/internal/managedservice/domain/service"

	"github.com/google/uuid"
)

type blueprintService struct {
	repo managedrepo.BlueprintRepository
}

func NewBlueprintService(repo managedrepo.BlueprintRepository) managedservice.BlueprintService {
	return &blueprintService{repo: repo}
}
func (s *blueprintService) CreateBlueprint(ctx context.Context, in *entity.CreateBlueprint) (*entity.BlueprintView, error) {
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
func (s *blueprintService) GetBlueprint(ctx context.Context, in *entity.GetBlueprint) (*entity.BlueprintView, error) {
	return s.repo.GetBlueprint(ctx, in)
}
func (s *blueprintService) GetBlueprintByVersion(ctx context.Context, in *entity.GetBlueprintByVersion) (*entity.BlueprintView, error) {
	return s.repo.GetBlueprintByVersion(ctx, in)
}
func (s *blueprintService) DeleteBlueprint(ctx context.Context, in *entity.DeleteBlueprint) error {
	if in.AuditID == uuid.Nil {
		auditID, err := uuid.NewV7()
		if err != nil {
			return err
		}
		in.AuditID = auditID
	}
	return s.repo.DeleteBlueprint(ctx, in)
}
