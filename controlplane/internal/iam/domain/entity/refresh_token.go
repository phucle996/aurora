package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

type RefreshToken struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	DeviceID      *uuid.UUID
	TokenHash     string
	TokenFamilyID uuid.UUID
	TenantID      *uuid.UUID
	IssuedAt      time.Time
	ExpiresAt     time.Time
}

type RefreshTokenSession struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	DeviceID      *uuid.UUID
	TokenHash     string
	TokenFamilyID uuid.UUID
	ExpiresAt     time.Time
}

type RefreshTokenUser struct {
	ID     uuid.UUID
	Status UserStatus
}

type RefreshTokenDevice struct {
	ID     uuid.UUID
	Status DeviceStatus
}

type RefreshTokenResult struct {
	AccessToken      string
	RefreshToken     string
	RuntimeDeviceID  string
	DeviceSecret     string
	TrackingID       string
	TrackedDeviceID  string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
}

// RefreshContext gói gộp dữ liệu refresh để giảm round-trip DB.
type RefreshContext struct {
	Session RefreshTokenSession
	User    RefreshTokenUser
	Device  *RefreshTokenDevice
}
