package repo

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type PersonalInstanceRepository interface {
	ListPersonalInstances(context.Context, *entity.ListPersonalInstances) (*entity.PersonalInstancePage, error)
	GetPersonalInstance(context.Context, *entity.GetPersonalInstance) (*entity.PersonalInstanceDetail, error)
	ListPersonalInstanceOperations(context.Context, *entity.ListPersonalInstanceOperations) (*entity.PersonalInstanceOperationPage, error)
	GetPersonalInstanceOperation(context.Context, *entity.GetPersonalInstanceOperation) (*entity.PersonalInstanceOperationDetail, error)
	RenamePersonalInstance(context.Context, *entity.RenamePersonalInstance) (*entity.RenamePersonalInstanceResult, error)
}
