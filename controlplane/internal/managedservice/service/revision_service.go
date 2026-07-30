package service

import (
	"context"
	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	managedservice "controlplane/internal/managedservice/domain/service"

	"github.com/google/uuid"
)

type revisionService struct {
	repo managedrepo.RevisionRepository
}

func NewRevisionService(repo managedrepo.RevisionRepository) managedservice.RevisionService {
	return &revisionService{repo: repo}
}
func (s *revisionService) CreateDraft(ctx context.Context, in *entity.CreateDraft) (*entity.DraftView, error) {
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
func (s *revisionService) GetDraft(ctx context.Context, in *entity.GetDraft) (*entity.DraftView, error) {
	return s.repo.GetDraft(ctx, in)
}
func (s *revisionService) ListRevisions(ctx context.Context, in *entity.ListRevisions) ([]entity.DraftView, error) {
	return s.repo.ListRevisions(ctx, in)
}
func (s *revisionService) PatchDraft(ctx context.Context, in *entity.PatchDraft) (*entity.DraftView, error) {
	if in.AuditID == uuid.Nil {
		auditID, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		in.AuditID = auditID
	}
	return s.repo.PatchDraft(ctx, in)
}
func (s *revisionService) ValidateDraft(ctx context.Context, in *entity.ValidateDraft) (*entity.DraftView, error) {
	if in.AuditID == uuid.Nil {
		auditID, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		in.AuditID = auditID
	}
	return s.repo.ValidateDraft(ctx, in)
}
func (s *revisionService) PublishDraft(ctx context.Context, in *entity.PublishDraft) (*entity.DraftView, error) {
	if in.AuditID == uuid.Nil {
		auditID, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		in.AuditID = auditID
	}
	return s.repo.PublishDraft(ctx, in)
}
func (s *revisionService) RetireRevision(ctx context.Context, in *entity.RetireRevision) (*entity.DraftView, error) {
	if in.AuditID == uuid.Nil {
		auditID, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		in.AuditID = auditID
	}
	return s.repo.RetireRevision(ctx, in)
}
func (s *revisionService) DeleteDraft(ctx context.Context, in *entity.DeleteDraft) error {
	if in.AuditID == uuid.Nil {
		auditID, err := uuid.NewV7()
		if err != nil {
			return err
		}
		in.AuditID = auditID
	}
	return s.repo.DeleteDraft(ctx, in)
}
