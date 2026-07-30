package repo

import (
	"context"
	"controlplane/internal/managedservice/domain/entity"
)

type VersionRepository interface {
	CreateVersion(context.Context, *entity.CreateVersion) (*entity.VersionView, error)
	ListVersions(context.Context, *entity.ListVersions) ([]entity.VersionView, error)
	GetVersion(context.Context, *entity.GetVersion) (*entity.VersionView, error)
	UpdateVersion(context.Context, *entity.UpdateVersion) (*entity.VersionView, error)
	DeprecateVersion(context.Context, *entity.DeprecateVersion) (*entity.VersionView, error)
	RetireVersion(context.Context, *entity.RetireVersion) (*entity.VersionView, error)
}
