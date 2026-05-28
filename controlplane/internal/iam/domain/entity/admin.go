package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

type AdminAPIKey struct {
	ID        uuid.UUID
	KeyHash   string
	CreatedBy *string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type AdminBootstrapPayload struct {
	Actor              string
	KeyHash            string
	ExpiresAt          time.Time
	RecoveryCodeHashes []string
	GeneratedAt        time.Time
	SecretCiphertext   string
	RequestPath        string
	RequestMethod      string
}

type AdminLoginRequest struct {
	RawAPIKey       string
	MFAMethod       MFAType
	MFACode         string
	DevicePublicKey string
	HostnameHint    string
	HostnameAlias   string
	ClientDeviceID  string
}

type AdminLoginResult struct {
	AdminAPIToken            string
	AccessKey                string
	AccessSecret             string
	ClientDeviceID           string
	ClientDeviceIDProvenance string
	ExpiresAt                time.Time
}

type Admin2FASettings struct {
	ID               uuid.UUID
	SecretCiphertext string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type AdminDeviceBindingInput struct {
	ID                   uuid.UUID
	DeviceName           string
	DeviceType           *string
	OSName               *string
	BrowserName          *string
	PublicKey            string
	PublicKeyFingerprint string
	LastSeenIP           *string
	LastSeenUserAgent    *string
	LastSeenAt           *time.Time
	Now                  time.Time
}

type AdminDevice struct {
	ID                   uuid.UUID
	DeviceName           string
	DeviceType           *string
	OSName               *string
	BrowserName          *string
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
