package repo

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type PersonalCatalogVersionRepository interface {
	GetPersonalCatalogVersion(context.Context, *entity.GetPersonalCatalogVersion) (*entity.PersonalCatalogVersionView, error)
}
