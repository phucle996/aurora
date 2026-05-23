package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

type MFAType string

type MFAStatus string

const (
	MFATypeTOTP         MFAType = "totp"
	MFATypeRecoveryCode MFAType = "recovery_code"
)

const (
	MFAStatusPending  MFAStatus = "pending"
	MFAStatusEnabled  MFAStatus = "enabled"
	MFAStatusDisabled MFAStatus = "disabled"
)

type MFASetting struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	Type             MFAType
	Status           MFAStatus
	SecretCiphertext *string
	SecretKeyID      *string
	Label            *string
	ConfirmedAt      *time.Time
	DisabledAt       *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type MFAChallenge struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	Status           ChallengeStatus
	AllowedMethods   []string
	ExpiresAt        time.Time
	VerifiedAt       *time.Time
	FailedAttempts   int
	CreatedIP        *string
	CreatedUserAgent *string
	CreatedAt        time.Time
}

type MFARecoveryCode struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	CodeHash  string
	CodeHint  *string
	UsedAt    *time.Time
	CreatedAt time.Time
}
