package storageSvcInterface

import (
	"context"

	storageEntity "controlplane/internal/storage/domain/entity"
	"github.com/google/uuid"
)

type PersonalStorageAccessSessionService interface {
	CreatePersonalStorageAccessSession(context.Context, *storageEntity.StorageAccessSession) error
	GetPersonalStorageAccessSessionStatus(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (*storageEntity.StorageAccessSessionStatus, error)
}
