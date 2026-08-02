package mocks

import (
	"context"

	entity "controlplane/internal/hierarchy/domain/entity"
)

type ZoneEncryptionKeyRepository struct {
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

	RegisterErr  error
	ListErr      error
	ActivateErr  error
	RetireErr    error
	ResolveErr   error
	ResolveCalls int
}

func (r *ZoneEncryptionKeyRepository) RegisterZoneEncryptionKey(_ context.Context, in *entity.RegisterZoneEncryptionKey) (*entity.RegisterZoneEncryptionKey, error) {
	r.Registered = in
	if r.RegisterResult != nil {
		return r.RegisterResult, r.RegisterErr
	}
	return in, r.RegisterErr
}

func (r *ZoneEncryptionKeyRepository) ListZoneEncryptionKeys(_ context.Context, in *entity.ListZoneEncryptionKeys) ([]entity.ListZoneEncryptionKeys, error) {
	r.Listed = in
	return r.ListResult, r.ListErr
}

func (r *ZoneEncryptionKeyRepository) ActivateZoneEncryptionKey(_ context.Context, in *entity.ActivateZoneEncryptionKey) (*entity.ActivateZoneEncryptionKey, error) {
	r.Activated = in
	if r.ActivateResult != nil {
		return r.ActivateResult, r.ActivateErr
	}
	return in, r.ActivateErr
}

func (r *ZoneEncryptionKeyRepository) RetireZoneEncryptionKey(_ context.Context, in *entity.RetireZoneEncryptionKey) (*entity.RetireZoneEncryptionKey, error) {
	r.Retired = in
	if r.RetireResult != nil {
		return r.RetireResult, r.RetireErr
	}
	return in, r.RetireErr
}

func (r *ZoneEncryptionKeyRepository) ResolveZonePayloadKey(_ context.Context, in *entity.ResolveZonePayloadKey) (*entity.ResolveZonePayloadKey, error) {
	r.ResolveCalls++
	r.Resolved = in
	if r.ResolveResult != nil {
		return r.ResolveResult, r.ResolveErr
	}
	return in, r.ResolveErr
}
