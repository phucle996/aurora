package service

import (
	"context"
	"controlplane/internal/managedservice/domain/entity"
)

type DefinitionService interface {
	CreateDefinition(context.Context, *entity.CreateDefinition) (*entity.DefinitionView, error)
	ListDefinitions(context.Context, *entity.ListDefinitions) ([]entity.DefinitionView, error)
	GetDefinition(context.Context, *entity.GetDefinition) (*entity.DefinitionView, error)
	UpdateDefinition(context.Context, *entity.UpdateDefinition) (*entity.DefinitionView, error)
	RetireDefinition(context.Context, *entity.RetireDefinition) (*entity.DefinitionView, error)
}
