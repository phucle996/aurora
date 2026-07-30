package service

import (
	"context"
	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	managedservice "controlplane/internal/managedservice/domain/service"

	"github.com/google/uuid"
)

type versionService struct{ repo managedrepo.VersionRepository }

func NewVersionService(repo managedrepo.VersionRepository) managedservice.VersionService {
	return &versionService{repo: repo}
}
func (s *versionService) CreateVersion(ctx context.Context, in *entity.CreateVersion) (*entity.VersionView, error) {
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
	return s.repo.CreateVersion(ctx, in)
}
func (s *versionService) ListVersions(ctx context.Context, in *entity.ListVersions) ([]entity.VersionView, error) {
	return s.repo.ListVersions(ctx, in)
}
func (s *versionService) GetVersion(ctx context.Context, in *entity.GetVersion) (*entity.VersionView, error) {
	return s.repo.GetVersion(ctx, in)
}
func (s *versionService) UpdateVersion(ctx context.Context, in *entity.UpdateVersion) (*entity.VersionView, error) {
	if in.AuditID == uuid.Nil {
		auditID, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		in.AuditID = auditID
	}
	return s.repo.UpdateVersion(ctx, in)
}
func (s *versionService) DeprecateVersion(ctx context.Context, in *entity.DeprecateVersion) (*entity.VersionView, error) {
	if in.AuditID == uuid.Nil {
		auditID, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		in.AuditID = auditID
	}
	return s.repo.DeprecateVersion(ctx, in)
}
func (s *versionService) RetireVersion(ctx context.Context, in *entity.RetireVersion) (*entity.VersionView, error) {
	if in.AuditID == uuid.Nil {
		auditID, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		in.AuditID = auditID
	}
	return s.repo.RetireVersion(ctx, in)
}
