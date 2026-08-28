package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

type AdminDevice struct {
	ID                   uuid.UUID
	DeviceName           string
	DeviceType           string
	PublicKey            string
	PublicKeyFingerprint string
	ClientDeviceID       *string
	QuarantinedAt        *time.Time
	RevokedAt            *time.Time
	LastSeenIP           *string
	LastSeenUserAgent    *string
	LastSeenAt           *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}
