package repo

import (
	"context"
	"controlplane/internal/managedservice/domain/entity"
)

type BlueprintRepository interface {
	CreateBlueprint(context.Context, *entity.CreateBlueprint) (*entity.BlueprintView, error)
	GetBlueprint(context.Context, *entity.GetBlueprint) (*entity.BlueprintView, error)
	GetBlueprintByVersion(context.Context, *entity.GetBlueprintByVersion) (*entity.BlueprintView, error)
	DeleteBlueprint(context.Context, *entity.DeleteBlueprint) error
}
