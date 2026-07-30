package repo

import (
	"context"
	"controlplane/internal/managedservice/domain/entity"
)

type CategoryRepository interface {
	CreateCategory(context.Context, *entity.CreateCategory) (*entity.CategoryView, error)
	ListCategories(context.Context, *entity.ListCategories) ([]entity.CategoryView, error)
	GetCategory(context.Context, *entity.GetCategory) (*entity.CategoryView, error)
	UpdateCategory(context.Context, *entity.UpdateCategory) (*entity.CategoryView, error)
	RetireCategory(context.Context, *entity.RetireCategory) (*entity.CategoryView, error)
}
