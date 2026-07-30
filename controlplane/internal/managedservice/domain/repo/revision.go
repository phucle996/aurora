package repo

import (
	"context"
	"controlplane/internal/managedservice/domain/entity"
)

type RevisionRepository interface {
	CreateDraft(context.Context, *entity.CreateDraft) (*entity.DraftView, error)
	GetDraft(context.Context, *entity.GetDraft) (*entity.DraftView, error)
	ListRevisions(context.Context, *entity.ListRevisions) ([]entity.DraftView, error)
	PatchDraft(context.Context, *entity.PatchDraft) (*entity.DraftView, error)
	ValidateDraft(context.Context, *entity.ValidateDraft) (*entity.DraftView, error)
	PublishDraft(context.Context, *entity.PublishDraft) (*entity.DraftView, error)
	RetireRevision(context.Context, *entity.RetireRevision) (*entity.DraftView, error)
	DeleteDraft(context.Context, *entity.DeleteDraft) error
}
