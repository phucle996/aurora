package mocks

import (
	"context"

	entity "controlplane/internal/hierarchy/domain/entity"
)

type ZoneEncryptionKeyService struct {
	RegisterCalls int
	ListCalls     int
	ActivateCalls int
	RetireCalls   int
	ResolveCalls  int

	Registered *entity.RegisterZoneEncryptionKey
	Listed     *entity.ListZoneEncryptionKeys
	Activated  *entity.ActivateZoneEncryptionKey
	Retired    *entity.RetireZoneEncryptionKey
	Resolved   *entity.ResolveZonePayloadKey

	RegisterResult *entity.RegisterZoneEncryptionKey
	ListResult     []entity.ListZoneEncryptionKeys
	ActivateResult *entity.ActivateZoneEncryptionKey
	RetireResult   *entity.RetireZoneEncryptionKey
	ResolveResult  *entity.ResolveZonePayloadKey

	RegisterErr error
	ListErr     error
	ActivateErr error
	RetireErr   error
	ResolveErr  error
}

func (s *ZoneEncryptionKeyService) RegisterZoneEncryptionKey(_ context.Context, in *entity.RegisterZoneEncryptionKey) (*entity.RegisterZoneEncryptionKey, error) {
	s.RegisterCalls++
	s.Registered = in
	return s.RegisterResult, s.RegisterErr
}

func (s *ZoneEncryptionKeyService) ListZoneEncryptionKeys(_ context.Context, in *entity.ListZoneEncryptionKeys) ([]entity.ListZoneEncryptionKeys, error) {
	s.ListCalls++
	s.Listed = in
	return s.ListResult, s.ListErr
}

func (s *ZoneEncryptionKeyService) ActivateZoneEncryptionKey(_ context.Context, in *entity.ActivateZoneEncryptionKey) (*entity.ActivateZoneEncryptionKey, error) {
	s.ActivateCalls++
	s.Activated = in
	return s.ActivateResult, s.ActivateErr
}

func (s *ZoneEncryptionKeyService) RetireZoneEncryptionKey(_ context.Context, in *entity.RetireZoneEncryptionKey) (*entity.RetireZoneEncryptionKey, error) {
	s.RetireCalls++
	s.Retired = in
	return s.RetireResult, s.RetireErr
}

func (s *ZoneEncryptionKeyService) ResolveZonePayloadKey(_ context.Context, in *entity.ResolveZonePayloadKey) (*entity.ResolveZonePayloadKey, error) {
	s.ResolveCalls++
	s.Resolved = in
	return s.ResolveResult, s.ResolveErr
}
