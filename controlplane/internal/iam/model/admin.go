package iamModel

import (
	"time"

	"github.com/google/uuid"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type AdminDevice struct {
	ID                   uuid.UUID  `db:"id"`
	DeviceName           string     `db:"device_name"`
	DeviceType           *string    `db:"device_type"`
	OSName               *string    `db:"os_name"`
	BrowserName          *string    `db:"browser_name"`
	PublicKey            string     `db:"public_key"`
	PublicKeyFingerprint string     `db:"public_key_fingerprint"`
	QuarantinedAt        *time.Time `db:"quarantined_at"`
	RevokedAt            *time.Time `db:"revoked_at"`
	LastSeenIP           *string    `db:"last_seen_ip"`
	LastSeenUserAgent    *string    `db:"last_seen_user_agent"`
	LastSeenAt           *time.Time `db:"last_seen_at"`
	CreatedAt            time.Time  `db:"created_at"`
	UpdatedAt            time.Time  `db:"updated_at"`
}

func AdminDeviceEntityToModel(input iamEntity.AdminDevice) AdminDevice {
	return AdminDevice{
		ID:                   input.ID,
		DeviceName:           input.DeviceName,
		DeviceType:           input.DeviceType,
		OSName:               input.OSName,
		BrowserName:          input.BrowserName,
		PublicKey:            input.PublicKey,
		PublicKeyFingerprint: input.PublicKeyFingerprint,
		QuarantinedAt:        input.QuarantinedAt,
		RevokedAt:            input.RevokedAt,
		LastSeenIP:           input.LastSeenIP,
		LastSeenUserAgent:    input.LastSeenUserAgent,
		LastSeenAt:           input.LastSeenAt,
		CreatedAt:            input.CreatedAt,
		UpdatedAt:            input.UpdatedAt,
	}
}

func AdminDeviceModelToEntity(input AdminDevice) iamEntity.AdminDevice {
	strID := input.ID.String()
	return iamEntity.AdminDevice{
		ID:                   input.ID,
		DeviceName:           input.DeviceName,
		DeviceType:           input.DeviceType,
		OSName:               input.OSName,
		BrowserName:          input.BrowserName,
		PublicKey:            input.PublicKey,
		PublicKeyFingerprint: input.PublicKeyFingerprint,
		ClientDeviceID:       &strID,
		QuarantinedAt:        input.QuarantinedAt,
		RevokedAt:            input.RevokedAt,
		LastSeenIP:           input.LastSeenIP,
		LastSeenUserAgent:    input.LastSeenUserAgent,
		LastSeenAt:           input.LastSeenAt,
		CreatedAt:            input.CreatedAt,
		UpdatedAt:            input.UpdatedAt,
	}
}
