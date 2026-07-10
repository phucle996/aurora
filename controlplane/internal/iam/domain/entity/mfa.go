package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

type MFASetting struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	SecretCiphertext *string
	SecretKeyID      *string
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
