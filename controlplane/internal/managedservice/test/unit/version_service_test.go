package unit

import (
	"context"
	"testing"

	"controlplane/internal/managedservice/domain/entity"
	managedserviceimpl "controlplane/internal/managedservice/service"
	"controlplane/internal/observability"

	"github.com/google/uuid"
)

type versionRepositorySpy struct {
	created    *entity.CreateVersion
	updated    *entity.UpdateVersion
	deprecated *entity.DeprecateVersion
	retired    *entity.RetireVersion
}

func (r *versionRepositorySpy) CreateVersion(_ context.Context, in *entity.CreateVersion) (*entity.VersionView, error) {
	r.created = in
	return nil, nil
}
func (r *versionRepositorySpy) ListVersions(context.Context, *entity.ListVersions) ([]entity.VersionView, error) {
	return nil, nil
}
func (r *versionRepositorySpy) GetVersion(context.Context, *entity.GetVersion) (*entity.VersionView, error) {
	return nil, nil
}
func (r *versionRepositorySpy) UpdateVersion(_ context.Context, in *entity.UpdateVersion) (*entity.VersionView, error) {
	r.updated = in
	return nil, nil
}
func (r *versionRepositorySpy) DeprecateVersion(_ context.Context, in *entity.DeprecateVersion) (*entity.VersionView, error) {
	r.deprecated = in
	return nil, nil
}
func (r *versionRepositorySpy) RetireVersion(_ context.Context, in *entity.RetireVersion) (*entity.VersionView, error) {
	r.retired = in
	return nil, nil
}

func TestVersionServiceOwnsSystemIdentifiers(t *testing.T) {
	repo := &versionRepositorySpy{}
	subject := managedserviceimpl.NewVersionService(repo, observability.NewNoopWorkflowRecorder())

	if _, err := subject.CreateVersion(context.Background(), &entity.CreateVersion{}); err != nil {
		t.Fatalf("create version: %v", err)
	}
	if repo.created == nil || repo.created.ID == uuid.Nil || repo.created.AuditID == uuid.Nil {
		t.Fatal("service must assign resource and audit identifiers before repository")
	}

	if _, err := subject.UpdateVersion(context.Background(), &entity.UpdateVersion{}); err != nil {
		t.Fatalf("update version: %v", err)
	}
	if repo.updated == nil || repo.updated.AuditID == uuid.Nil {
		t.Fatal("service must assign update audit identifier before repository")
	}

	if _, err := subject.DeprecateVersion(context.Background(), &entity.DeprecateVersion{}); err != nil {
		t.Fatalf("deprecate version: %v", err)
	}
	if repo.deprecated == nil || repo.deprecated.AuditID == uuid.Nil {
		t.Fatal("service must assign deprecate audit identifier before repository")
	}

	if _, err := subject.RetireVersion(context.Background(), &entity.RetireVersion{}); err != nil {
		t.Fatalf("retire version: %v", err)
	}
	if repo.retired == nil || repo.retired.AuditID == uuid.Nil {
		t.Fatal("service must assign retire audit identifier before repository")
	}
}
