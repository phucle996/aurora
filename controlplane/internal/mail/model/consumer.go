package mailModel

import (
	mailEntity "controlplane/internal/mail/domain/entity"
	"time"
)

type Consumer struct {
	ID              string                    `db:"id"`
	TenantID        string                    `db:"tenant_id"`
	Name            string                    `db:"name"`
	SourceType      mailEntity.SourceType     `db:"source_type"`
	SourceConfigRef string                    `db:"source_config_ref"`
	Status          mailEntity.ConsumerStatus `db:"status"`
	Parallelism     int                       `db:"parallelism"`
	CreatedAt       time.Time                 `db:"created_at"`
	UpdatedAt       time.Time                 `db:"updated_at"`
}

func ConsumerEntityToModel(e mailEntity.Consumer) Consumer {
	return Consumer{
		ID:              e.ID,
		TenantID:        e.TenantID,
		Name:            e.Name,
		SourceType:      e.SourceType,
		SourceConfigRef: e.SourceConfigRef,
		Status:          e.Status,
		Parallelism:     e.Parallelism,
		CreatedAt:       e.CreatedAt,
		UpdatedAt:       e.UpdatedAt,
	}
}

func ConsumerModelToEntity(m Consumer) mailEntity.Consumer {
	return mailEntity.Consumer{
		ID:              m.ID,
		TenantID:        m.TenantID,
		Name:            m.Name,
		SourceType:      m.SourceType,
		SourceConfigRef: m.SourceConfigRef,
		Status:          m.Status,
		Parallelism:     m.Parallelism,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}
}
