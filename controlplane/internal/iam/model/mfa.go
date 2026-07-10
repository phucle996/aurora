package iamModel

import (
	"time"

	"github.com/google/uuid"

	"controlplane/internal/iam/domain/entity"
)

type MFASetting struct {
	ID               uuid.UUID  `db:"id"`
	UserID           uuid.UUID  `db:"user_id"`
	SecretCiphertext *string    `db:"secret_ciphertext"`
	SecretKeyID      *string    `db:"secret_key_id"`
	DisabledAt       *time.Time `db:"disabled_at"`
	CreatedAt        time.Time  `db:"created_at"`
	UpdatedAt        time.Time  `db:"updated_at"`
}

func MFASettingEntityToModel(input iamEntity.MFASetting) MFASetting {
	return MFASetting{
		ID:               input.ID,
		UserID:           input.UserID,
		SecretCiphertext: input.SecretCiphertext,
		SecretKeyID:      input.SecretKeyID,
		DisabledAt:       input.DisabledAt,
		CreatedAt:        input.CreatedAt,
		UpdatedAt:        input.UpdatedAt}
}
func MFASettingModelToEntity(input MFASetting) iamEntity.MFASetting {
	return iamEntity.MFASetting{
		ID:               input.ID,
		UserID:           input.UserID,
		SecretCiphertext: input.SecretCiphertext,
		SecretKeyID:      input.SecretKeyID,
		DisabledAt:       input.DisabledAt,
		CreatedAt:        input.CreatedAt,
		UpdatedAt:        input.UpdatedAt}
}

type MFAChallenge struct {
	ID               uuid.UUID  `db:"id"`
	UserID           uuid.UUID  `db:"user_id"`
	Status           string     `db:"status"`
	AllowedMethods   []string   `db:"allowed_methods"`
	ExpiresAt        time.Time  `db:"expires_at"`
	VerifiedAt       *time.Time `db:"verified_at"`
	FailedAttempts   int        `db:"failed_attempts"`
	CreatedIP        *string    `db:"created_ip"`
	CreatedUserAgent *string    `db:"created_user_agent"`
	CreatedAt        time.Time  `db:"created_at"`
}

func MFAChallengeEntityToModel(input iamEntity.MFAChallenge) MFAChallenge {
	return MFAChallenge{
		ID:               input.ID,
		UserID:           input.UserID,
		Status:           string(input.Status),
		AllowedMethods:   input.AllowedMethods,
		ExpiresAt:        input.ExpiresAt,
		VerifiedAt:       input.VerifiedAt,
		FailedAttempts:   input.FailedAttempts,
		CreatedIP:        input.CreatedIP,
		CreatedUserAgent: input.CreatedUserAgent,
		CreatedAt:        input.CreatedAt}
}
func MFAChallengeModelToEntity(input MFAChallenge) iamEntity.MFAChallenge {
	return iamEntity.MFAChallenge{
		ID:               input.ID,
		UserID:           input.UserID,
		Status:           iamEntity.ChallengeStatus(input.Status),
		AllowedMethods:   input.AllowedMethods,
		ExpiresAt:        input.ExpiresAt,
		VerifiedAt:       input.VerifiedAt,
		FailedAttempts:   input.FailedAttempts,
		CreatedIP:        input.CreatedIP,
		CreatedUserAgent: input.CreatedUserAgent,
		CreatedAt:        input.CreatedAt}
}

type MFARecoveryCode struct {
	ID        uuid.UUID  `db:"id"`
	UserID    uuid.UUID  `db:"user_id"`
	CodeHash  string     `db:"code_hash"`
	CodeHint  *string    `db:"code_hint"`
	UsedAt    *time.Time `db:"used_at"`
	CreatedAt time.Time  `db:"created_at"`
}

func MFARecoveryCodeEntityToModel(input iamEntity.MFARecoveryCode) MFARecoveryCode {
	return MFARecoveryCode{ID: input.ID,
		UserID:    input.UserID,
		CodeHash:  input.CodeHash,
		CodeHint:  input.CodeHint,
		UsedAt:    input.UsedAt,
		CreatedAt: input.CreatedAt}
}
func MFARecoveryCodeModelToEntity(input MFARecoveryCode) iamEntity.MFARecoveryCode {
	return iamEntity.MFARecoveryCode{ID: input.ID,
		UserID:    input.UserID,
		CodeHash:  input.CodeHash,
		CodeHint:  input.CodeHint,
		UsedAt:    input.UsedAt,
		CreatedAt: input.CreatedAt}
}
