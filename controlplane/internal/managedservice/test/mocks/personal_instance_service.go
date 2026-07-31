package mocks

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type PersonalInstanceService struct {
	CreateCalls         int
	CreateInput         *entity.CreatePersonalInstance
	CreateResult        *entity.CreatePersonalInstanceResult
	CreateErr           error
	ListCalls           int
	ListInput           *entity.ListPersonalInstances
	ListResult          *entity.PersonalInstancePage
	ListErr             error
	GetCalls            int
	GetInput            *entity.GetPersonalInstance
	GetResult           *entity.PersonalInstanceDetail
	GetErr              error
	ListOperationCalls  int
	ListOperationInput  *entity.ListPersonalInstanceOperations
	ListOperationResult *entity.PersonalInstanceOperationPage
	ListOperationErr    error
	GetOperationCalls   int
	GetOperationInput   *entity.GetPersonalInstanceOperation
	GetOperationResult  *entity.PersonalInstanceOperationDetail
	GetOperationErr     error
	RenameCalls         int
	RenameInput         *entity.RenamePersonalInstance
	RenameResult        *entity.RenamePersonalInstanceResult
	RenameErr           error
	ResizeCalls         int
	ResizeInput         *entity.ResizePersonalInstance
	ResizeResult        *entity.ResizePersonalInstanceResult
	ResizeErr           error
	DeleteCalls         int
	DeleteInput         *entity.DeletePersonalInstance
	DeleteResult        *entity.DeletePersonalInstanceResult
	DeleteErr           error
	RetryCalls          int
	RetryInput          *entity.RetryPersonalInstance
	RetryResult         *entity.RetryPersonalInstanceResult
	RetryErr            error
}

func (s *PersonalInstanceService) CreatePersonalInstance(_ context.Context, in *entity.CreatePersonalInstance) (*entity.CreatePersonalInstanceResult, error) {
	s.CreateCalls++
	s.CreateInput = in
	return s.CreateResult, s.CreateErr
}

func (s *PersonalInstanceService) ListPersonalInstances(_ context.Context, in *entity.ListPersonalInstances) (*entity.PersonalInstancePage, error) {
	s.ListCalls++
	s.ListInput = in
	return s.ListResult, s.ListErr
}

func (s *PersonalInstanceService) GetPersonalInstance(_ context.Context, in *entity.GetPersonalInstance) (*entity.PersonalInstanceDetail, error) {
	s.GetCalls++
	s.GetInput = in
	return s.GetResult, s.GetErr
}

func (s *PersonalInstanceService) ListPersonalInstanceOperations(_ context.Context, in *entity.ListPersonalInstanceOperations) (*entity.PersonalInstanceOperationPage, error) {
	s.ListOperationCalls++
	s.ListOperationInput = in
	return s.ListOperationResult, s.ListOperationErr
}

func (s *PersonalInstanceService) GetPersonalInstanceOperation(_ context.Context, in *entity.GetPersonalInstanceOperation) (*entity.PersonalInstanceOperationDetail, error) {
	s.GetOperationCalls++
	s.GetOperationInput = in
	return s.GetOperationResult, s.GetOperationErr
}

func (s *PersonalInstanceService) RenamePersonalInstance(_ context.Context, in *entity.RenamePersonalInstance) (*entity.RenamePersonalInstanceResult, error) {
	s.RenameCalls++
	s.RenameInput = in
	return s.RenameResult, s.RenameErr
}

func (s *PersonalInstanceService) ResizePersonalInstance(_ context.Context, in *entity.ResizePersonalInstance) (*entity.ResizePersonalInstanceResult, error) {
	s.ResizeCalls++
	s.ResizeInput = in
	return s.ResizeResult, s.ResizeErr
}
func (s *PersonalInstanceService) DeletePersonalInstance(_ context.Context, in *entity.DeletePersonalInstance) (*entity.DeletePersonalInstanceResult, error) {
	s.DeleteCalls++
	s.DeleteInput = in
	return s.DeleteResult, s.DeleteErr
}
func (s *PersonalInstanceService) RetryPersonalInstance(_ context.Context, in *entity.RetryPersonalInstance) (*entity.RetryPersonalInstanceResult, error) {
	s.RetryCalls++
	s.RetryInput = in
	return s.RetryResult, s.RetryErr
}
