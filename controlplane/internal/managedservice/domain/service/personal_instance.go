package service

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type PersonalInstanceService interface {
	CreatePersonalInstance(context.Context, *entity.CreatePersonalInstance) (*entity.CreatePersonalInstanceResult, error)
	ListPersonalInstances(context.Context, *entity.ListPersonalInstances) (*entity.PersonalInstancePage, error)
	GetPersonalInstance(context.Context, *entity.GetPersonalInstance) (*entity.PersonalInstanceDetail, error)
	ListPersonalInstanceOperations(context.Context, *entity.ListPersonalInstanceOperations) (*entity.PersonalInstanceOperationPage, error)
	GetPersonalInstanceOperation(context.Context, *entity.GetPersonalInstanceOperation) (*entity.PersonalInstanceOperationDetail, error)
	RenamePersonalInstance(context.Context, *entity.RenamePersonalInstance) (*entity.RenamePersonalInstanceResult, error)
	ResizePersonalInstance(context.Context, *entity.ResizePersonalInstance) (*entity.ResizePersonalInstanceResult, error)
	DeletePersonalInstance(context.Context, *entity.DeletePersonalInstance) (*entity.DeletePersonalInstanceResult, error)
	RetryPersonalInstance(context.Context, *entity.RetryPersonalInstance) (*entity.RetryPersonalInstanceResult, error)
}
