package iamModel

import (
	"time"

	"github.com/google/uuid"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type MFASetting struct {
	ID               uuid.UUID `db:"id"`
	UserID           uuid.UUID `db:"user_id"`
	SecretCiphertext string    `db:"secret_ciphertext"`
	SecretKeyID      string    `db:"secret_key_id"`
	CreatedAt        time.Time `db:"created_at"`
	UpdatedAt        time.Time `db:"updated_at"`
}

func MFASettingEntityToModel(input iamEntity.MFASetting) MFASetting {
	return MFASetting{
		ID:               input.ID,
		UserID:           input.UserID,
		SecretCiphertext: input.SecretCiphertext,
		SecretKeyID:      input.SecretKeyID,
		CreatedAt:        input.CreatedAt,
		UpdatedAt:        input.UpdatedAt,
	}
}

func MFASettingModelToEntity(input MFASetting) iamEntity.MFASetting {
	return iamEntity.MFASetting{
		ID:               input.ID,
		UserID:           input.UserID,
		SecretCiphertext: input.SecretCiphertext,
		SecretKeyID:      input.SecretKeyID,
		CreatedAt:        input.CreatedAt,
		UpdatedAt:        input.UpdatedAt,
	}
}

type MFARecoveryCode struct {
	ID           uuid.UUID `db:"id"`
	MFASettingID uuid.UUID `db:"mfa_setting_id"`
	CodeHash     string    `db:"code_hash"`
	CodeHint     *string   `db:"code_hint"`
	CreatedAt    time.Time `db:"created_at"`
}

func MFARecoveryCodeEntityToModel(input iamEntity.MFARecoveryCode) MFARecoveryCode {
	return MFARecoveryCode{
		ID:           input.ID,
		MFASettingID: input.MFASettingID,
		CodeHash:     input.CodeHash,
		CodeHint:     input.CodeHint,
		CreatedAt:    input.CreatedAt,
	}
}

func MFARecoveryCodeModelToEntity(input MFARecoveryCode) iamEntity.MFARecoveryCode {
	return iamEntity.MFARecoveryCode{
		ID:           input.ID,
		MFASettingID: input.MFASettingID,
		CodeHash:     input.CodeHash,
		CodeHint:     input.CodeHint,
		CreatedAt:    input.CreatedAt,
	}
}
