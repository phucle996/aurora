package mailModel

import (
	mailEntity "controlplane/internal/mail/domain/entity"
	"time"
)

type Gateway struct {
	ID          string    `db:"id"`
	TenantID    string    `db:"tenant_id"`
	Name        string    `db:"name"`
	RoutePolicy string    `db:"route_policy"`
	IsActive    bool      `db:"is_active"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

func GatewayEntityToModel(e mailEntity.Gateway) Gateway {
	return Gateway{
		ID:          e.ID,
		TenantID:    e.TenantID,
		Name:        e.Name,
		RoutePolicy: e.RoutePolicy,
		IsActive:    e.IsActive,
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
}

func GatewayModelToEntity(m Gateway) mailEntity.Gateway {
	return mailEntity.Gateway{
		ID:          m.ID,
		TenantID:    m.TenantID,
		Name:        m.Name,
		RoutePolicy: m.RoutePolicy,
		IsActive:    m.IsActive,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}
