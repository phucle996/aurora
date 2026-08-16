package storageSvcInterface

import (
	"context"

	storageEntity "controlplane/internal/storage/domain/entity"
)

type CommercialAdmissionZonePublisher interface {
	Publish(context.Context, storageEntity.CommercialAdmissionZoneDelivery) error
}

type CommercialAdmissionZoneRelayService interface {
	RelayBatch(context.Context) (int, error)
}
