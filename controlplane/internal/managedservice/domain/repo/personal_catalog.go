package repo

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type PersonalCatalogRepository interface {
	ListPersonalCatalog(context.Context, *entity.ListPersonalCatalog) (*entity.PersonalCatalogPage, error)
}
