package iamModel

import (
	"time"

	"github.com/google/uuid"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type AdminAPIKey struct {
	ID        uuid.UUID `db:"id"`
	KeyHash   string    `db:"key_hash"`
	CreatedBy *string   `db:"created_by"`
	CreatedAt time.Time `db:"created_at"`
	ExpiresAt time.Time `db:"expires_at"`
}

func AdminAPIKeyEntityToModel(input iamEntity.AdminAPIKey) AdminAPIKey {
	return AdminAPIKey{
		ID:        input.ID,
		KeyHash:   input.KeyHash,
		CreatedBy: input.CreatedBy,
		CreatedAt: input.CreatedAt,
		ExpiresAt: input.ExpiresAt,
	}
}

func AdminAPIKeyModelToEntity(input AdminAPIKey) iamEntity.AdminAPIKey {
	return iamEntity.AdminAPIKey{
		ID:        input.ID,
		KeyHash:   input.KeyHash,
		CreatedBy: input.CreatedBy,
		CreatedAt: input.CreatedAt,
		ExpiresAt: input.ExpiresAt,
	}
}
