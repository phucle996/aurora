package hierarchySvcInterface

import (
	"context"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
)

type ZoneEncryptionKeyService interface {
	RegisterZoneEncryptionKey(context.Context, *hierarchyEntity.RegisterZoneEncryptionKey) (*hierarchyEntity.RegisterZoneEncryptionKey, error)
	ListZoneEncryptionKeys(context.Context, *hierarchyEntity.ListZoneEncryptionKeys) ([]hierarchyEntity.ListZoneEncryptionKeys, error)
	ActivateZoneEncryptionKey(context.Context, *hierarchyEntity.ActivateZoneEncryptionKey) (*hierarchyEntity.ActivateZoneEncryptionKey, error)
	RetireZoneEncryptionKey(context.Context, *hierarchyEntity.RetireZoneEncryptionKey) (*hierarchyEntity.RetireZoneEncryptionKey, error)
	ResolveZonePayloadKey(context.Context, *hierarchyEntity.ResolveZonePayloadKey) (*hierarchyEntity.ResolveZonePayloadKey, error)
}
