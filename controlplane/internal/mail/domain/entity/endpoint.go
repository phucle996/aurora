package mailEntity

import (
	"time"

	"github.com/google/uuid"
)

// TLSMode đại diện cho chế độ bảo mật kết nối TLS của mail server (none, starttls, tls, mtls)
type TLSMode string

const (
	TLSModeNone     TLSMode = "none"
	TLSModeStartTLS TLSMode = "starttls"
	TLSModeTLS      TLSMode = "tls"
	TLSModeMTLS     TLSMode = "mtls"
)

type Endpoint struct {
	ID             uuid.UUID
	ZoneID         uuid.UUID
	Name           string
	Host           string
	Port           int
	Username       string
	Password       string
	TLSMode        TLSMode
	Status         string
	MaxConnections int
	Priority       int
	Weight         int
	CACertPEM      string
	ClientCertPEM  string
	ClientKeyPEM   string
	IsActive       bool
	CreatedAt      *time.Time
	UpdatedAt      *time.Time
}

// CreateEndpointParams groups the inputs required to construct a new Endpoint.
type CreateEndpointParams struct {
	ZoneID         uuid.UUID
	Name           string
	Host           string
	Port           int
	Username       string
	Password       string
	TLSMode        TLSMode
	Status         string
	MaxConnections int
	Priority       int
	Weight         int
	CACertPEM      string
	ClientCertPEM  string
	ClientKeyPEM   string
}

// UpdateEndpointParams groups the inputs required to modify an existing Endpoint.
type UpdateEndpointParams struct {
	ZoneID         uuid.UUID
	ID             uuid.UUID
	Name           string
	Host           string
	Port           int
	Username       string
	Password       string
	TLSMode        TLSMode
	Status         string
	MaxConnections int
	Priority       int
	Weight         int
	CACertPEM      string
	ClientCertPEM  string
	ClientKeyPEM   string
	IsActive       bool
}

// TestConnection đại diện cho thông tin cấu hình cần thiết để kiểm tra kết nối SMTP thô
type TestConnection struct {
	ZoneID        uuid.UUID
	Host          string
	Port          int
	Username      string
	Password      string
	TLSMode       TLSMode
	CACertPEM     *string
	ClientCertPEM *string
	ClientKeyPEM  *string
}
