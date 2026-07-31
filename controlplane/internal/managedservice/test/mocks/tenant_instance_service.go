package mocks

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type TenantInstanceService struct {
	CreateCalls         int
	CreateInput         *entity.CreateTenantInstance
	CreateResult        *entity.CreateTenantInstanceResult
	CreateErr           error
	ListCalls           int
	ListInput           *entity.ListTenantInstances
	ListResult          *entity.TenantInstancePage
	ListErr             error
	GetCalls            int
	GetInput            *entity.GetTenantInstance
	GetResult           *entity.TenantInstanceDetail
	GetErr              error
	ListOperationCalls  int
	ListOperationInput  *entity.ListTenantInstanceOperations
	ListOperationResult *entity.TenantInstanceOperationPage
	ListOperationErr    error
	GetOperationCalls   int
	GetOperationInput   *entity.GetTenantInstanceOperation
	GetOperationResult  *entity.TenantInstanceOperationDetail
	GetOperationErr     error
	RenameCalls         int
	RenameInput         *entity.RenameTenantInstance
	RenameResult        *entity.RenameTenantInstanceResult
	RenameErr           error
	ResizeCalls         int
	ResizeInput         *entity.ResizeTenantInstance
	ResizeResult        *entity.ResizeTenantInstanceResult
	ResizeErr           error
	DeleteCalls         int
	DeleteInput         *entity.DeleteTenantInstance
	DeleteResult        *entity.DeleteTenantInstanceResult
	DeleteErr           error
	RetryCalls          int
	RetryInput          *entity.RetryTenantInstance
	RetryResult         *entity.RetryTenantInstanceResult
	RetryErr            error
}

func (s *TenantInstanceService) CreateTenantInstance(_ context.Context, in *entity.CreateTenantInstance) (*entity.CreateTenantInstanceResult, error) {
	s.CreateCalls++
	s.CreateInput = in
	return s.CreateResult, s.CreateErr
}

func (s *TenantInstanceService) ListTenantInstances(_ context.Context, in *entity.ListTenantInstances) (*entity.TenantInstancePage, error) {
	s.ListCalls++
	s.ListInput = in
	return s.ListResult, s.ListErr
}

func (s *TenantInstanceService) GetTenantInstance(_ context.Context, in *entity.GetTenantInstance) (*entity.TenantInstanceDetail, error) {
	s.GetCalls++
	s.GetInput = in
	return s.GetResult, s.GetErr
}

func (s *TenantInstanceService) ListTenantInstanceOperations(_ context.Context, in *entity.ListTenantInstanceOperations) (*entity.TenantInstanceOperationPage, error) {
	s.ListOperationCalls++
	s.ListOperationInput = in
	return s.ListOperationResult, s.ListOperationErr
}

func (s *TenantInstanceService) GetTenantInstanceOperation(_ context.Context, in *entity.GetTenantInstanceOperation) (*entity.TenantInstanceOperationDetail, error) {
	s.GetOperationCalls++
	s.GetOperationInput = in
	return s.GetOperationResult, s.GetOperationErr
}

func (s *TenantInstanceService) RenameTenantInstance(_ context.Context, in *entity.RenameTenantInstance) (*entity.RenameTenantInstanceResult, error) {
	s.RenameCalls++
	s.RenameInput = in
	return s.RenameResult, s.RenameErr
}

func (s *TenantInstanceService) ResizeTenantInstance(_ context.Context, in *entity.ResizeTenantInstance) (*entity.ResizeTenantInstanceResult, error) {
	s.ResizeCalls++
	s.ResizeInput = in
	return s.ResizeResult, s.ResizeErr
}
func (s *TenantInstanceService) DeleteTenantInstance(_ context.Context, in *entity.DeleteTenantInstance) (*entity.DeleteTenantInstanceResult, error) {
	s.DeleteCalls++
	s.DeleteInput = in
	return s.DeleteResult, s.DeleteErr
}
func (s *TenantInstanceService) RetryTenantInstance(_ context.Context, in *entity.RetryTenantInstance) (*entity.RetryTenantInstanceResult, error) {
	s.RetryCalls++
	s.RetryInput = in
	return s.RetryResult, s.RetryErr
}
