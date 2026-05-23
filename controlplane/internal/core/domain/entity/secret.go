package coreEntity

import "time"

type SecretStatus string

const (
	SecretStatusPending SecretStatus = "pending"
	SecretStatusActive  SecretStatus = "active"
	SecretStatusRetired SecretStatus = "retired"
	SecretStatusRevoked SecretStatus = "revoked"
)

type SecretFamily struct {
	ID          string
	Code        string
	Name        string
	Description string
	CreatedAt   time.Time
}

type SecretVersion struct {
	ID                string
	FamilyID          string
	Version           int
	SecretCiphertext  string
	SecretFingerprint string
	Status            SecretStatus
	IsPrimary         bool
	NotBefore         time.Time
	NotAfter          *time.Time
	ActivatedAt       *time.Time
	RetiredAt         *time.Time
	RevokedAt         *time.Time
	RotationReason    string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type RotationPlan struct {
	Family           SecretFamily
	CurrentVersions  []SecretVersion
	RotationTTL      time.Duration
	RotationInterval time.Duration
	RotateAt         time.Time
}

type BootstrapSecretFamily struct {
	Code        string
	Name        string
	Description string
}

type EnsureInitialSecretResult struct {
	Family      SecretFamily
	Version     SecretVersion
	Created     bool
	PlainSecret string
}

type RotateSecretFamilyInput struct {
	FamilyCode        string
	TTL               time.Duration
	NewVersion        *SecretVersion
	RetirePreviousNow bool
}
