package service

import (
	"context"
	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	managedservice "controlplane/internal/managedservice/domain/service"

	"github.com/google/uuid"
)

type categoryService struct {
	repo managedrepo.CategoryRepository
}

func NewCategoryService(repo managedrepo.CategoryRepository) managedservice.CategoryService {
	return &categoryService{repo: repo}
}
func (s *categoryService) CreateCategory(ctx context.Context, in *entity.CreateCategory) (*entity.CategoryView, error) {
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
	return s.repo.CreateCategory(ctx, in)
}
func (s *categoryService) ListCategories(ctx context.Context, in *entity.ListCategories) ([]entity.CategoryView, error) {
	return s.repo.ListCategories(ctx, in)
}
func (s *categoryService) GetCategory(ctx context.Context, in *entity.GetCategory) (*entity.CategoryView, error) {
	return s.repo.GetCategory(ctx, in)
}
func (s *categoryService) UpdateCategory(ctx context.Context, in *entity.UpdateCategory) (*entity.CategoryView, error) {
	if in.AuditID == uuid.Nil {
		auditID, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		in.AuditID = auditID
	}
	return s.repo.UpdateCategory(ctx, in)
}
func (s *categoryService) RetireCategory(ctx context.Context, in *entity.RetireCategory) (*entity.CategoryView, error) {
	if in.AuditID == uuid.Nil {
		auditID, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		in.AuditID = auditID
	}
	return s.repo.RetireCategory(ctx, in)
}
