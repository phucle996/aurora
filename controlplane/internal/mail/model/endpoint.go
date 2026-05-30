package mailModel

import (
	"time"

	mailEntity "controlplane/internal/mail/domain/entity"

	"github.com/google/uuid"
)

type Endpoint struct {
	ID               uuid.UUID               `db:"id"`
	ZoneID           uuid.UUID               `db:"zone_id"`
	Name             string                  `db:"name"`
	Provider         mailEntity.ProviderType `db:"provider"`
	ConnectionConfig []byte                  `db:"connection_config"`
	IsActive         bool                    `db:"is_active"`
	CreatedAt        time.Time               `db:"created_at"`
	UpdatedAt        time.Time               `db:"updated_at"`
}

func EndpointEntityToModel(e mailEntity.Endpoint, encryptedConfig []byte) Endpoint {
	return Endpoint{
		ID:               e.ID,
		ZoneID:           e.ZoneID,
		Name:             e.Name,
		Provider:         e.Provider,
		ConnectionConfig: encryptedConfig,
		IsActive:         e.IsActive,
		CreatedAt:        e.CreatedAt,
		UpdatedAt:        e.UpdatedAt,
	}
}

func EndpointModelToEntity(m Endpoint, plainConfig map[string]interface{}) mailEntity.Endpoint {
	return mailEntity.Endpoint{
		ID:               m.ID,
		ZoneID:           m.ZoneID,
		Name:             m.Name,
		Provider:         m.Provider,
		ConnectionConfig: plainConfig,
		IsActive:         m.IsActive,
		CreatedAt:        m.CreatedAt,
		UpdatedAt:        m.UpdatedAt,
	}
}
