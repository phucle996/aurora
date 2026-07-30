package service

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type PersonalCatalogService interface {
	ListPersonalCatalog(context.Context, *entity.ListPersonalCatalog) (*entity.PersonalCatalogPage, error)
}
