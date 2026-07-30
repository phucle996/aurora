package unit

import (
	"context"
	"testing"

	"controlplane/internal/managedservice/domain/entity"
	managedserviceimpl "controlplane/internal/managedservice/service"

	"github.com/google/uuid"
)

type categoryRepositorySpy struct {
	created *entity.CreateCategory
	updated *entity.UpdateCategory
	retired *entity.RetireCategory
}

func (r *categoryRepositorySpy) CreateCategory(_ context.Context, in *entity.CreateCategory) (*entity.CategoryView, error) {
	r.created = in
	return nil, nil
}
func (r *categoryRepositorySpy) ListCategories(context.Context, *entity.ListCategories) ([]entity.CategoryView, error) {
	return nil, nil
}
func (r *categoryRepositorySpy) GetCategory(context.Context, *entity.GetCategory) (*entity.CategoryView, error) {
	return nil, nil
}
func (r *categoryRepositorySpy) UpdateCategory(_ context.Context, in *entity.UpdateCategory) (*entity.CategoryView, error) {
	r.updated = in
	return nil, nil
}
func (r *categoryRepositorySpy) RetireCategory(_ context.Context, in *entity.RetireCategory) (*entity.CategoryView, error) {
	r.retired = in
	return nil, nil
}

func TestCategoryServiceOwnsSystemIdentifiers(t *testing.T) {
	repo := &categoryRepositorySpy{}
	subject := managedserviceimpl.NewCategoryService(repo)

	created := &entity.CreateCategory{}
	if _, err := subject.CreateCategory(context.Background(), created); err != nil {
		t.Fatalf("create category: %v", err)
	}
	if repo.created == nil || repo.created.ID == uuid.Nil || repo.created.AuditID == uuid.Nil {
		t.Fatal("service must assign resource and audit identifiers before repository")
	}

	updated := &entity.UpdateCategory{}
	if _, err := subject.UpdateCategory(context.Background(), updated); err != nil {
		t.Fatalf("update category: %v", err)
	}
	if repo.updated == nil || repo.updated.AuditID == uuid.Nil {
		t.Fatal("service must assign update audit identifier before repository")
	}

	retired := &entity.RetireCategory{}
	if _, err := subject.RetireCategory(context.Background(), retired); err != nil {
		t.Fatalf("retire category: %v", err)
	}
	if repo.retired == nil || repo.retired.AuditID == uuid.Nil {
		t.Fatal("service must assign retire audit identifier before repository")
	}
}

func TestCategoryServicePreservesExistingIdentifiers(t *testing.T) {
	repo := &categoryRepositorySpy{}
	subject := managedserviceimpl.NewCategoryService(repo)
	resourceID := uuid.MustParse("10000000-0000-7000-8000-000000000001")
	auditID := uuid.MustParse("10000000-0000-7000-8000-000000000002")
	in := &entity.CreateCategory{ID: resourceID, AuditID: auditID}

	if _, err := subject.CreateCategory(context.Background(), in); err != nil {
		t.Fatalf("create category: %v", err)
	}
	if repo.created.ID != resourceID || repo.created.AuditID != auditID {
		t.Fatal("service must preserve identities supplied by a retried internal workflow")
	}
}
