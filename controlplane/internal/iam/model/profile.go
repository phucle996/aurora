package iamModel

import (
	"time"

	"github.com/google/uuid"

	"controlplane/internal/iam/domain/entity"
)

type UserProfile struct {
	UserID       uuid.UUID `db:"user_id"`
	Username     string    `db:"username"`
	AccountEmail string    `db:"account_email"`
	Phone        *string   `db:"phone"`
	Fullname     string    `db:"fullname"`
	Address      *string   `db:"address"`
	AvatarURL    *string   `db:"avatar_url"`
	Bio          *string   `db:"bio"`
	Locale       string    `db:"locale"`
	Timezone     string    `db:"timezone"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
}

func UserProfileEntityToModel(input iamEntity.UserProfile) UserProfile {
	return UserProfile{
		UserID:       input.UserID,
		Username:     input.Username,
		AccountEmail: input.AccountEmail,
		Phone:        input.Phone,
		Fullname:     input.Fullname,
		Address:      input.Address,
		AvatarURL:    input.AvatarURL,
		Bio:          input.Bio,
		Locale:       input.Locale,
		Timezone:     input.Timezone,
		CreatedAt:    input.CreatedAt,
		UpdatedAt:    input.UpdatedAt,
	}
}

func UserProfileModelToEntity(input UserProfile) iamEntity.UserProfile {
	return iamEntity.UserProfile{
		UserID:       input.UserID,
		Username:     input.Username,
		AccountEmail: input.AccountEmail,
		Phone:        input.Phone,
		Fullname:     input.Fullname,
		Address:      input.Address,
		AvatarURL:    input.AvatarURL,
		Bio:          input.Bio,
		Locale:       input.Locale,
		Timezone:     input.Timezone,
		CreatedAt:    input.CreatedAt,
		UpdatedAt:    input.UpdatedAt,
	}
}
