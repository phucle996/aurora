package storageRepoInterface

import (
	"context"

	storageEntity "controlplane/internal/storage/domain/entity"
)

type CommercialAdmissionProjectionRepository interface {
	Apply(
		context.Context,
		*storageEntity.CommercialAdmissionProjection,
	) error
}

type CommercialAdmissionZonePayloadEncoder interface {
	Encode(*storageEntity.CommercialAdmissionZoneProjection) ([]byte, error)
}
