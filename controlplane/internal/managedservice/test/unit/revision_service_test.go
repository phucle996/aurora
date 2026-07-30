package unit

import (
	"context"
	"testing"

	"controlplane/internal/managedservice/domain/entity"
	managedserviceimpl "controlplane/internal/managedservice/service"
	"controlplane/internal/observability"

	"github.com/google/uuid"
)

type revisionRepositorySpy struct {
	created   *entity.CreateDraft
	patched   *entity.PatchDraft
	validated *entity.ValidateDraft
	published *entity.PublishDraft
	retired   *entity.RetireRevision
	deleted   *entity.DeleteDraft
}

func (r *revisionRepositorySpy) CreateDraft(_ context.Context, in *entity.CreateDraft) (*entity.DraftView, error) {
	r.created = in
	return nil, nil
}
func (r *revisionRepositorySpy) GetDraft(context.Context, *entity.GetDraft) (*entity.DraftView, error) {
	return nil, nil
}
func (r *revisionRepositorySpy) ListRevisions(context.Context, *entity.ListRevisions) ([]entity.DraftView, error) {
	return nil, nil
}
func (r *revisionRepositorySpy) PatchDraft(_ context.Context, in *entity.PatchDraft) (*entity.DraftView, error) {
	r.patched = in
	return nil, nil
}
func (r *revisionRepositorySpy) ValidateDraft(_ context.Context, in *entity.ValidateDraft) (*entity.DraftView, error) {
	r.validated = in
	return nil, nil
}
func (r *revisionRepositorySpy) PublishDraft(_ context.Context, in *entity.PublishDraft) (*entity.DraftView, error) {
	r.published = in
	return nil, nil
}
func (r *revisionRepositorySpy) RetireRevision(_ context.Context, in *entity.RetireRevision) (*entity.DraftView, error) {
	r.retired = in
	return nil, nil
}
func (r *revisionRepositorySpy) DeleteDraft(_ context.Context, in *entity.DeleteDraft) error {
	r.deleted = in
	return nil
}

func TestRevisionServiceOwnsSystemIdentifiers(t *testing.T) {
	repo := &revisionRepositorySpy{}
	subject := managedserviceimpl.NewRevisionService(repo, observability.NewNoopWorkflowRecorder())

	if _, err := subject.CreateDraft(context.Background(), &entity.CreateDraft{}); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if repo.created == nil || repo.created.ID == uuid.Nil || repo.created.AuditID == uuid.Nil {
		t.Fatal("service must assign resource and audit identifiers before repository")
	}

	if _, err := subject.PatchDraft(context.Background(), &entity.PatchDraft{}); err != nil {
		t.Fatalf("patch draft: %v", err)
	}
	if repo.patched == nil || repo.patched.AuditID == uuid.Nil {
		t.Fatal("service must assign patch audit identifier before repository")
	}

	if _, err := subject.ValidateDraft(context.Background(), &entity.ValidateDraft{}); err != nil {
		t.Fatalf("validate draft: %v", err)
	}
	if repo.validated == nil || repo.validated.AuditID == uuid.Nil {
		t.Fatal("service must assign validation audit identifier before repository")
	}

	if _, err := subject.PublishDraft(context.Background(), &entity.PublishDraft{}); err != nil {
		t.Fatalf("publish draft: %v", err)
	}
	if repo.published == nil || repo.published.AuditID == uuid.Nil {
		t.Fatal("service must assign publish audit identifier before repository")
	}

	if _, err := subject.RetireRevision(context.Background(), &entity.RetireRevision{}); err != nil {
		t.Fatalf("retire revision: %v", err)
	}
	if repo.retired == nil || repo.retired.AuditID == uuid.Nil {
		t.Fatal("service must assign retire audit identifier before repository")
	}

	if err := subject.DeleteDraft(context.Background(), &entity.DeleteDraft{}); err != nil {
		t.Fatalf("delete draft: %v", err)
	}
	if repo.deleted == nil || repo.deleted.AuditID == uuid.Nil {
		t.Fatal("service must assign delete audit identifier before repository")
	}
}
