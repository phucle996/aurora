package mailEntity

import (
	"time"

	"github.com/google/uuid"
)

type ProviderType string

const (
	SMTP     ProviderType = "smtp"
	SendGrid ProviderType = "sendgrid"
	Mailgun  ProviderType = "mailgun"
)

type Endpoint struct {
	ID               uuid.UUID
	ZoneID           uuid.UUID
	Name             string
	Provider         ProviderType
	ConnectionConfig map[string]interface{}
	IsActive         bool
	CreatedAt        *time.Time
	UpdatedAt        *time.Time
}

// CreateEndpointParams groups the inputs required to construct a new Endpoint.
type CreateEndpointParams struct {
	ZoneID           uuid.UUID
	Name             string
	Provider         ProviderType
	ConnectionConfig map[string]interface{}
}

// UpdateEndpointParams groups the inputs required to modify an existing Endpoint.
type UpdateEndpointParams struct {
	ZoneID           uuid.UUID
	ID               uuid.UUID
	Name             string
	ConnectionConfig map[string]interface{}
	IsActive         bool
}
