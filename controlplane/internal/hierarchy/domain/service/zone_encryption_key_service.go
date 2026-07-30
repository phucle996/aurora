package service

import (
	"context"

	entity "controlplane/internal/hierarchy/domain/entity"
)

type ZoneEncryptionKeyService interface {
	RegisterZoneEncryptionKey(context.Context, *entity.RegisterZoneEncryptionKey) (*entity.RegisterZoneEncryptionKey, error)
	ListZoneEncryptionKeys(context.Context, *entity.ListZoneEncryptionKeys) ([]entity.ListZoneEncryptionKeys, error)
	ActivateZoneEncryptionKey(context.Context, *entity.ActivateZoneEncryptionKey) (*entity.ActivateZoneEncryptionKey, error)
	RetireZoneEncryptionKey(context.Context, *entity.RetireZoneEncryptionKey) (*entity.RetireZoneEncryptionKey, error)
}
