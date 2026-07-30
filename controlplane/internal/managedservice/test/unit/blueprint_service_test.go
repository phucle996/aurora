package unit

import (
	"context"
	"testing"

	"controlplane/internal/managedservice/domain/entity"
	managedserviceimpl "controlplane/internal/managedservice/service"

	"github.com/google/uuid"
)

type blueprintRepositorySpy struct {
	created *entity.CreateBlueprint
	deleted *entity.DeleteBlueprint
}

func (r *blueprintRepositorySpy) CreateBlueprint(_ context.Context, in *entity.CreateBlueprint) (*entity.BlueprintView, error) {
	r.created = in
	return nil, nil
}
func (r *blueprintRepositorySpy) GetBlueprint(context.Context, *entity.GetBlueprint) (*entity.BlueprintView, error) {
	return nil, nil
}
func (r *blueprintRepositorySpy) GetBlueprintByVersion(context.Context, *entity.GetBlueprintByVersion) (*entity.BlueprintView, error) {
	return nil, nil
}
func (r *blueprintRepositorySpy) DeleteBlueprint(_ context.Context, in *entity.DeleteBlueprint) error {
	r.deleted = in
	return nil
}

func TestBlueprintServiceOwnsSystemIdentifiers(t *testing.T) {
	repo := &blueprintRepositorySpy{}
	subject := managedserviceimpl.NewBlueprintService(repo)

	if _, err := subject.CreateBlueprint(context.Background(), &entity.CreateBlueprint{}); err != nil {
		t.Fatalf("create blueprint: %v", err)
	}
	if repo.created == nil || repo.created.ID == uuid.Nil || repo.created.AuditID == uuid.Nil {
		t.Fatal("service must assign resource and audit identifiers before repository")
	}

	if err := subject.DeleteBlueprint(context.Background(), &entity.DeleteBlueprint{}); err != nil {
		t.Fatalf("delete blueprint: %v", err)
	}
	if repo.deleted == nil || repo.deleted.AuditID == uuid.Nil {
		t.Fatal("service must assign delete audit identifier before repository")
	}
}
