package storageRepoInterface

import (
	"context"

	storageEntity "controlplane/internal/storage/domain/entity"
	"github.com/google/uuid"
)

type PersonalStorageAccessSessionRepository interface {
	GetPersonalStorageAccessSessionTarget(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (string, error)
	CreatePersonalStorageAccessSession(context.Context, *storageEntity.StorageAccessSession, *storageEntity.StorageOutboxRecord) error
	GetPersonalStorageAccessSessionStatus(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (*storageEntity.StorageAccessSessionStatus, error)
}
