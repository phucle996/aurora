package iamModel

import (
	"time"

	"github.com/google/uuid"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type Device struct {
	ID                   string     `db:"id"`
	UserID               uuid.UUID  `db:"user_id"`
	DeviceName           string     `db:"device_name"`
	DeviceType           *string    `db:"device_type"`
	OSName               *string    `db:"os_name"`
	BrowserName          *string    `db:"browser_name"`
	PublicKey            string     `db:"public_key"`
	PublicKeyFingerprint string     `db:"public_key_fingerprint"`
	ClientDeviceID       *string    `db:"client_device_id"`
	RiskFlags            []byte     `db:"risk_flags"`
	RevokedAt            *time.Time `db:"revoked_at"`
	LastSeenIP           *string    `db:"last_seen_ip"`
	LastSeenUserAgent    *string    `db:"last_seen_user_agent"`
	LastSeenAt           *time.Time `db:"last_seen_at"`
	CreatedAt            time.Time  `db:"created_at"`
	UpdatedAt            time.Time  `db:"updated_at"`
}

// [COMMENT]: DeviceEntityToModel chuyển đổi domain entity sang db model
func DeviceEntityToModel(input iamEntity.Device) Device {
	return Device{
		ID:                   input.ID,
		UserID:               input.UserID,
		DeviceName:           input.DeviceName,
		DeviceType:           input.DeviceType,
		OSName:               input.OSName,
		BrowserName:          input.BrowserName,
		PublicKey:            input.PublicKey,
		PublicKeyFingerprint: input.PublicKeyFingerprint,
		ClientDeviceID:       input.ClientDeviceID,
		RiskFlags:            nil,
		RevokedAt:            input.RevokedAt,
		LastSeenIP:           input.LastSeenIP,
		LastSeenUserAgent:    input.LastSeenUserAgent,
		LastSeenAt:           input.LastSeenAt,
		CreatedAt:            input.CreatedAt,
		UpdatedAt:            input.UpdatedAt,
	}
}

// [COMMENT]: DeviceModelToEntity chuyển đổi db model sang domain entity
func DeviceModelToEntity(input Device) iamEntity.Device {
	return iamEntity.Device{
		ID:                   input.ID,
		UserID:               input.UserID,
		DeviceName:           input.DeviceName,
		DeviceType:           input.DeviceType,
		OSName:               input.OSName,
		BrowserName:          input.BrowserName,
		PublicKey:            input.PublicKey,
		PublicKeyFingerprint: input.PublicKeyFingerprint,
		ClientDeviceID:       input.ClientDeviceID,
		RiskFlags:            nil,
		RevokedAt:            input.RevokedAt,
		LastSeenIP:           input.LastSeenIP,
		LastSeenUserAgent:    input.LastSeenUserAgent,
		LastSeenAt:           input.LastSeenAt,
		CreatedAt:            input.CreatedAt,
		UpdatedAt:            input.UpdatedAt,
	}
}
