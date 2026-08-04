package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

// IssueDeviceRefreshToken is the durable credential issued to one tracked
// device. Runtime tenant, Zone and role context never crosses this boundary.
type IssueDeviceRefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	DeviceID  uuid.UUID
	TokenHash string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// RecoverUserSession is isolated from tenant switching. RequestedTenantID is
// an untrusted context request; the repository resolves current authority from
// the refresh credential's user and device in one PostgreSQL snapshot.
type RecoverUserSession struct {
	TokenHash         string
	RequestedTenantID *uuid.UUID
	Now               time.Time

	CredentialValid            bool
	ContextAuthorized          bool
	PersonalFallbackAuthorized bool
	UserID                     uuid.UUID
	DeviceID                   uuid.UUID
	ClientDeviceID             string
	Username                   string
	ResolvedTenantID           *uuid.UUID
	RoleLevel                  int32
}
