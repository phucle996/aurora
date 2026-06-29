package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

type DeviceStatus string

type ChallengeStatus string

const (
	DeviceStatusNew        DeviceStatus = "new"
	DeviceStatusRecognized DeviceStatus = "recognized"
	DeviceStatusTrusted    DeviceStatus = "trusted"
	DeviceStatusSuspicious DeviceStatus = "suspicious"
	DeviceStatusRevoked    DeviceStatus = "revoked"
)

const (
	ChallengeStatusPending  ChallengeStatus = "pending"
	ChallengeStatusVerified ChallengeStatus = "verified"
	ChallengeStatusExpired  ChallengeStatus = "expired"
	ChallengeStatusFailed   ChallengeStatus = "failed"
	ChallengeStatusConsumed ChallengeStatus = "consumed"
)

type Device struct {
	ID                   string
	UserID               uuid.UUID
	DeviceName           string
	DeviceType           *string
	OSName               *string
	BrowserName          *string
	PublicKey            string
	PublicKeyFingerprint string
	ClientDeviceID       *string
	Status               DeviceStatus
	TrustedAt            *time.Time
	QuarantinedAt        *time.Time
	RiskFlags            map[string]any
	RevokedAt            *time.Time
	LastSeenIP           *string
	LastSeenUserAgent    *string
	LastSeenAt           *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type DevicePresence struct {
	Device     Device
	IsOnline   bool
	LastSeenAt *time.Time
	LastIP     *string
	LastUA     *string
}

type DeviceListResult struct {
	Devices []DevicePresence
	Total   int64
}
