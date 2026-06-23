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
	TenantID      *uuid.UUID
	IssuedAt      time.Time
	ExpiresAt     time.Time
	UsedAt        *time.Time
	RevokedAt     *time.Time
}

type RefreshTokenSession struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	DeviceID      *uuid.UUID
	TokenHash     string
	TenantID      *uuid.UUID
	ExpiresAt     time.Time
	UsedAt        *time.Time
	RevokedAt     *time.Time
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
	AccessKey        string
	AccessSecret     string
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
