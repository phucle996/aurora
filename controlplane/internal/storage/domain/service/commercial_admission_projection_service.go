package storageSvcInterface

import (
	"context"

	storageEntity "controlplane/internal/storage/domain/entity"
)

type CommercialAdmissionProjectionService interface {
	Apply(context.Context, *storageEntity.CommercialAdmissionProjectionCommand) error
}
