package mocks

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type PersonalInstanceService struct {
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
