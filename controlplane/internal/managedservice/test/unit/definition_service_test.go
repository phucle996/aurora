package unit

import (
	"context"
	"testing"

	"controlplane/internal/managedservice/domain/entity"
	managedserviceimpl "controlplane/internal/managedservice/service"

	"github.com/google/uuid"
)

type definitionRepositorySpy struct {
	created *entity.CreateDefinition
	updated *entity.UpdateDefinition
	retired *entity.RetireDefinition
}

func (r *definitionRepositorySpy) CreateDefinition(_ context.Context, in *entity.CreateDefinition) (*entity.DefinitionView, error) {
	r.created = in
	return nil, nil
}
func (r *definitionRepositorySpy) ListDefinitions(context.Context, *entity.ListDefinitions) ([]entity.DefinitionView, error) {
	return nil, nil
}
func (r *definitionRepositorySpy) GetDefinition(context.Context, *entity.GetDefinition) (*entity.DefinitionView, error) {
	return nil, nil
}
func (r *definitionRepositorySpy) UpdateDefinition(_ context.Context, in *entity.UpdateDefinition) (*entity.DefinitionView, error) {
	r.updated = in
	return nil, nil
}
func (r *definitionRepositorySpy) RetireDefinition(_ context.Context, in *entity.RetireDefinition) (*entity.DefinitionView, error) {
	r.retired = in
	return nil, nil
}

func TestDefinitionServiceOwnsSystemIdentifiers(t *testing.T) {
	repo := &definitionRepositorySpy{}
	subject := managedserviceimpl.NewDefinitionService(repo)

	if _, err := subject.CreateDefinition(context.Background(), &entity.CreateDefinition{}); err != nil {
		t.Fatalf("create definition: %v", err)
	}
	if repo.created == nil || repo.created.ID == uuid.Nil || repo.created.AuditID == uuid.Nil {
		t.Fatal("service must assign resource and audit identifiers before repository")
	}

	if _, err := subject.UpdateDefinition(context.Background(), &entity.UpdateDefinition{}); err != nil {
		t.Fatalf("update definition: %v", err)
	}
	if repo.updated == nil || repo.updated.AuditID == uuid.Nil {
		t.Fatal("service must assign update audit identifier before repository")
	}

	if _, err := subject.RetireDefinition(context.Background(), &entity.RetireDefinition{}); err != nil {
		t.Fatalf("retire definition: %v", err)
	}
	if repo.retired == nil || repo.retired.AuditID == uuid.Nil {
		t.Fatal("service must assign retire audit identifier before repository")
	}
}
