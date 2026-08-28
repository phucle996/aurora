package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

type ChallengeStatus string

const (
	ChallengeStatusPending  ChallengeStatus = "pending"
	ChallengeStatusVerified ChallengeStatus = "verified"
	ChallengeStatusExpired  ChallengeStatus = "expired"
	ChallengeStatusFailed   ChallengeStatus = "failed"
	ChallengeStatusConsumed ChallengeStatus = "consumed"
)

// Device is the durable device record registered by a successful login.
type Device struct {
	ID                   uuid.UUID
	UserID               uuid.UUID
	DeviceName           string
	DeviceType           string
	PublicKey            string
	PublicKeyFingerprint string
	ClientDeviceID       *uuid.UUID
	RiskFlags            map[string]any
	RevokedAt            *time.Time
	LastSeenIP           *string
	LastSeenUserAgent    *string
	LastSeenAt           *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// DevicePresence is the flat durable projection for the self device list.
type DevicePresence struct {
	ID         uuid.UUID
	DeviceName string
	IsOnline   bool
	LastSeenAt *time.Time
	LastIP     *string
	LastUA     *string
	RevokedAt  *time.Time
}

// DeviceListResult belongs only to the self device list workflow.
type DeviceListResult struct {
	Devices []DevicePresence
	Total   int64
}

// DeviceRuntimeRevokeDevice is the flat durable command for revoking one
// non-current device owned by the verified self user.
type DeviceRuntimeRevokeDevice struct {
	UserID          uuid.UUID
	ClientDeviceID  uuid.UUID
	CurrentDeviceID uuid.UUID
}

// DeviceRuntimeRevokeOthers is the flat durable command for revoking every
// device except the verified current device.
type DeviceRuntimeRevokeOthers struct {
	UserID          uuid.UUID
	CurrentDeviceID uuid.UUID
}

// DeviceRuntimeRevokeResult is the durable result of one-device revocation.
type DeviceRuntimeRevokeResult struct {
	TargetExists  bool
	CurrentDevice bool
	Affected      int64
}

// DeviceRuntimeRevokeOthersResult is the durable result of the logout-other-
// devices workflow.
type DeviceRuntimeRevokeOthersResult struct {
	RevokedDeviceIDs []uuid.UUID
	Affected         int64
}

// DevicePresenceUpdate is one normalized heartbeat accepted by the advisory
// device-presence projection workflow.
type DevicePresenceUpdate struct {
	DeviceID          string
	LastSeenAt        int64
	LastSeenIP        string
	LastSeenUserAgent string
}
