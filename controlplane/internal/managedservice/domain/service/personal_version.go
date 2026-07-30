package service

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type PersonalCatalogVersionService interface {
	GetPersonalCatalogVersion(context.Context, *entity.GetPersonalCatalogVersion) (*entity.PersonalCatalogVersionView, error)
}
