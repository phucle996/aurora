package mailModel

import (
	"time"

	mailEntity "controlplane/internal/mail/domain/entity"

	"github.com/google/uuid"
)

type Endpoint struct {
	ID             uuid.UUID `db:"id"`
	ZoneID         uuid.UUID `db:"zone_id"`
	Name           string    `db:"name"`
	Host           string    `db:"host"`
	Port           int       `db:"port"`
	Username       string    `db:"username"`
	Password       string    `db:"password"`
	TLSMode        string    `db:"tls_mode"`
	Status         string    `db:"status"`
	MaxConnections int       `db:"max_connections"`
	Priority       int       `db:"priority"`
	Weight         int       `db:"weight"`
	CACertPEM      string    `db:"ca_cert_pem"`
	ClientCertPEM  string    `db:"client_cert_pem"`
	ClientKeyPEM   string    `db:"client_key_pem"`
	IsActive       bool      `db:"is_active"`
	CreatedAt      *time.Time `db:"created_at"`
	UpdatedAt      *time.Time `db:"updated_at"`
}

func EndpointEntityToModel(e mailEntity.Endpoint) Endpoint {
	return Endpoint{
		ID:             e.ID,
		ZoneID:         e.ZoneID,
		Name:           e.Name,
		Host:           e.Host,
		Port:           e.Port,
		Username:       e.Username,
		Password:       e.Password,
		TLSMode:        e.TLSMode,
		Status:         e.Status,
		MaxConnections: e.MaxConnections,
		Priority:       e.Priority,
		Weight:         e.Weight,
		CACertPEM:      e.CACertPEM,
		ClientCertPEM:  e.ClientCertPEM,
		ClientKeyPEM:   e.ClientKeyPEM,
		IsActive:       e.IsActive,
		CreatedAt:      e.CreatedAt,
		UpdatedAt:      e.UpdatedAt,
	}
}

func EndpointModelToEntity(m Endpoint) mailEntity.Endpoint {
	return mailEntity.Endpoint{
		ID:             m.ID,
		ZoneID:         m.ZoneID,
		Name:           m.Name,
		Host:           m.Host,
		Port:           m.Port,
		Username:       m.Username,
		Password:       m.Password,
		TLSMode:        m.TLSMode,
		Status:         m.Status,
		MaxConnections: m.MaxConnections,
		Priority:       m.Priority,
		Weight:         m.Weight,
		CACertPEM:      m.CACertPEM,
		ClientCertPEM:  m.ClientCertPEM,
		ClientKeyPEM:   m.ClientKeyPEM,
		IsActive:       m.IsActive,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}
