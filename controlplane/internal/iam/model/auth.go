package iamModel

import (
	"time"

	"github.com/google/uuid"

	"controlplane/internal/iam/domain/entity"
)

type User struct {
	ID           uuid.UUID `db:"id"`
	Username     string    `db:"username"`
	Email        string    `db:"email"`
	Phone        *string   `db:"phone"`
	PasswordHash string    `db:"password_hash"`
	Status       string    `db:"status"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
}

func UserEntityToModel(input iamEntity.User) User {
	return User{ID: input.ID,
		Username:     input.Username,
		Email:        input.Email,
		Phone:        input.Phone,
		PasswordHash: input.PasswordHash,
		Status:       string(input.Status),
		CreatedAt:    input.CreatedAt,
		UpdatedAt:    input.UpdatedAt}
}

func UserModelToEntity(input User) iamEntity.User {
	return iamEntity.User{
		ID:           input.ID,
		Username:     input.Username,
		Email:        input.Email,
		Phone:        input.Phone,
		PasswordHash: input.PasswordHash,
		Status:       iamEntity.UserStatus(input.Status),
		CreatedAt:    input.CreatedAt,
		UpdatedAt:    input.UpdatedAt}
}

type PasswordHistory struct {
	ID           uuid.UUID `db:"id"`
	UserID       uuid.UUID `db:"user_id"`
	PasswordHash string    `db:"password_hash"`
	CreatedAt    time.Time `db:"created_at"`
}

func PasswordHistoryEntityToModel(input iamEntity.PasswordHistory) PasswordHistory {
	return PasswordHistory{ID: input.ID,
		UserID:       input.UserID,
		PasswordHash: input.PasswordHash,
		CreatedAt:    input.CreatedAt}
}

func PasswordHistoryModelToEntity(input PasswordHistory) iamEntity.PasswordHistory {
	return iamEntity.PasswordHistory{ID: input.ID,
		UserID:       input.UserID,
		PasswordHash: input.PasswordHash,
		CreatedAt:    input.CreatedAt}
}

type RefreshToken struct {
	ID        uuid.UUID  `db:"id"`
	UserID    uuid.UUID  `db:"user_id"`
	DeviceID  *uuid.UUID `db:"device_id"`
	TokenHash string     `db:"token_hash"`
	TenantID  *uuid.UUID `db:"tenant_id"`
	IssuedAt  time.Time  `db:"issued_at"`
	ExpiresAt time.Time  `db:"expires_at"`
	UsedAt    *time.Time `db:"used_at"`
	RevokedAt *time.Time `db:"revoked_at"`
}

func RefreshTokenEntityToModel(input iamEntity.RefreshToken) RefreshToken {
	return RefreshToken{ID: input.ID,
		UserID:    input.UserID,
		DeviceID:  input.DeviceID,
		TokenHash: input.TokenHash,
		TenantID:  input.TenantID,
		IssuedAt:  input.IssuedAt,
		ExpiresAt: input.ExpiresAt,
		UsedAt:    input.UsedAt,
		RevokedAt: input.RevokedAt}
}

func RefreshTokenModelToEntity(input RefreshToken) iamEntity.RefreshToken {
	return iamEntity.RefreshToken{ID: input.ID,
		UserID:    input.UserID,
		DeviceID:  input.DeviceID,
		TokenHash: input.TokenHash,
		TenantID:  input.TenantID,
		IssuedAt:  input.IssuedAt,
		ExpiresAt: input.ExpiresAt,
		UsedAt:    input.UsedAt,
		RevokedAt: input.RevokedAt}
}
