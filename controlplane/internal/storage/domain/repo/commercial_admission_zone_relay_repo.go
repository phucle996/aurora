package storageRepoInterface

import (
	"context"

	storageEntity "controlplane/internal/storage/domain/entity"

	"github.com/google/uuid"
)

type CommercialAdmissionZoneRelayRepository interface {
	Claim(context.Context, uuid.UUID, int) ([]storageEntity.CommercialAdmissionZoneDelivery, error)
	Release(context.Context, storageEntity.CommercialAdmissionZoneDelivery, string) error
	MarkPublished(context.Context, storageEntity.CommercialAdmissionZoneDelivery) error
}
