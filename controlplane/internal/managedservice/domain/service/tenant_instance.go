package service

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type TenantInstanceService interface {
	CreateTenantInstance(context.Context, *entity.CreateTenantInstance) (*entity.CreateTenantInstanceResult, error)
	ListTenantInstances(context.Context, *entity.ListTenantInstances) (*entity.TenantInstancePage, error)
	GetTenantInstance(context.Context, *entity.GetTenantInstance) (*entity.TenantInstanceDetail, error)
	ListTenantInstanceOperations(context.Context, *entity.ListTenantInstanceOperations) (*entity.TenantInstanceOperationPage, error)
	GetTenantInstanceOperation(context.Context, *entity.GetTenantInstanceOperation) (*entity.TenantInstanceOperationDetail, error)
	RenameTenantInstance(context.Context, *entity.RenameTenantInstance) (*entity.RenameTenantInstanceResult, error)
	ResizeTenantInstance(context.Context, *entity.ResizeTenantInstance) (*entity.ResizeTenantInstanceResult, error)
	DeleteTenantInstance(context.Context, *entity.DeleteTenantInstance) (*entity.DeleteTenantInstanceResult, error)
	RetryTenantInstance(context.Context, *entity.RetryTenantInstance) (*entity.RetryTenantInstanceResult, error)
}
