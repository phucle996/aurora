package coreModel

import (
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
)

type SecretFamily struct {
	ID          string    `db:"id"`
	Code        string    `db:"code"`
	Name        string    `db:"name"`
	Description *string   `db:"description"`
	CreatedAt   time.Time `db:"created_at"`
}

type SecretVersion struct {
	ID                string     `db:"id"`
	FamilyID          string     `db:"family_id"`
	Version           int        `db:"version"`
	SecretCiphertext  string     `db:"secret_ciphertext"`
	SecretFingerprint string     `db:"secret_fingerprint"`
	Status            string     `db:"status"`
	IsPrimary         bool       `db:"is_primary"`
	NotBefore         time.Time  `db:"not_before"`
	NotAfter          *time.Time `db:"not_after"`
	ActivatedAt       *time.Time `db:"activated_at"`
	RetiredAt         *time.Time `db:"retired_at"`
	RevokedAt         *time.Time `db:"revoked_at"`
	RotationReason    *string    `db:"rotation_reason"`
	CreatedAt         time.Time  `db:"created_at"`
	UpdatedAt         time.Time  `db:"updated_at"`
}

func SecretFamilyEntityToModel(value coreEntity.SecretFamily) SecretFamily {
	description := value.Description
	return SecretFamily{
		ID:          value.ID,
		Code:        value.Code,
		Name:        value.Name,
		Description: nullableString(description),
		CreatedAt:   value.CreatedAt,
	}
}

func SecretFamilyModelToEntity(m SecretFamily) coreEntity.SecretFamily {
	return coreEntity.SecretFamily{
		ID:          m.ID,
		Code:        m.Code,
		Name:        m.Name,
		Description: derefString(m.Description),
		CreatedAt:   m.CreatedAt,
	}
}

func SecretVersionModelToEntity(m SecretVersion) coreEntity.SecretVersion {
	return coreEntity.SecretVersion{
		ID:                m.ID,
		FamilyID:          m.FamilyID,
		Version:           m.Version,
		SecretCiphertext:  m.SecretCiphertext,
		SecretFingerprint: m.SecretFingerprint,
		Status:            coreEntity.SecretStatus(m.Status),
		IsPrimary:         m.IsPrimary,
		NotBefore:         m.NotBefore,
		NotAfter:          m.NotAfter,
		ActivatedAt:       m.ActivatedAt,
		RetiredAt:         m.RetiredAt,
		RevokedAt:         m.RevokedAt,
		RotationReason:    derefString(m.RotationReason),
		CreatedAt:         m.CreatedAt,
		UpdatedAt:         m.UpdatedAt,
	}
}

func SecretVersionEntityToModel(e coreEntity.SecretVersion) SecretVersion {
	return SecretVersion{
		ID:                e.ID,
		FamilyID:          e.FamilyID,
		Version:           e.Version,
		SecretCiphertext:  e.SecretCiphertext,
		SecretFingerprint: e.SecretFingerprint,
		Status:            string(e.Status),
		IsPrimary:         e.IsPrimary,
		NotBefore:         e.NotBefore,
		NotAfter:          e.NotAfter,
		ActivatedAt:       e.ActivatedAt,
		RetiredAt:         e.RetiredAt,
		RevokedAt:         e.RevokedAt,
		RotationReason:    nullableString(e.RotationReason),
		CreatedAt:         e.CreatedAt,
		UpdatedAt:         e.UpdatedAt,
	}
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	v := value
	return &v
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
