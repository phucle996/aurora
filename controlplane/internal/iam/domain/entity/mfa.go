package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

type MFASetting struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	SecretCiphertext string
	SecretKeyID      string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type MFAStatus string

const (
	MFAStatusEnabled  MFAStatus = "enabled"
	MFAStatusDisabled MFAStatus = "disabled"
)

type MFAUserStatus struct {
	Status                 MFAStatus
	EnabledAt              *time.Time
	RecoveryCodesRemaining int
}

type MFASetupResult struct {
	SetupID         uuid.UUID
	ProvisioningURI string
	ManualSecret    string
	ExpiresAt       time.Time
}

type MFAConfirmationResult struct {
	EnabledAt     time.Time
	RecoveryCodes []string
}

type MFARecoveryCode struct {
	ID           uuid.UUID
	MFASettingID uuid.UUID
	CodeHash     string
	CodeHint     *string
	CreatedAt    time.Time
}

type MFALoginRequest struct {
	UserID          uuid.UUID
	MFASettingID    uuid.UUID
	Username        string
	TenantDomain    string
	Method          string
	Code            string
	DevicePublicKey string
	TrustDevice     bool
	DeviceName      string
	DeviceType      string
	ClientDeviceID  uuid.UUID
	RemoteIP        string
	UserAgent       string
}
