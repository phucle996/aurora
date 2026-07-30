package mocks

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type RevisionService struct {
	ValidateCalls int
	Validated     *entity.ValidateDraft
	ValidateView  *entity.DraftView
	ValidateError error
}

func (m *RevisionService) CreateDraft(context.Context, *entity.CreateDraft) (*entity.DraftView, error) {
	return nil, nil
}
func (m *RevisionService) GetDraft(context.Context, *entity.GetDraft) (*entity.DraftView, error) {
	return nil, nil
}
func (m *RevisionService) ListRevisions(context.Context, *entity.ListRevisions) ([]entity.DraftView, error) {
	return nil, nil
}
func (m *RevisionService) PatchDraft(context.Context, *entity.PatchDraft) (*entity.DraftView, error) {
	return nil, nil
}
func (m *RevisionService) ValidateDraft(_ context.Context, in *entity.ValidateDraft) (*entity.DraftView, error) {
	m.ValidateCalls++
	m.Validated = in
	return m.ValidateView, m.ValidateError
}
func (m *RevisionService) PublishDraft(context.Context, *entity.PublishDraft) (*entity.DraftView, error) {
	return nil, nil
}
func (m *RevisionService) RetireRevision(context.Context, *entity.RetireRevision) (*entity.DraftView, error) {
	return nil, nil
}
func (m *RevisionService) DeleteDraft(context.Context, *entity.DeleteDraft) error { return nil }
