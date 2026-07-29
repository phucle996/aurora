package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

type UserProfile struct {
	UserID       uuid.UUID
	Username     string
	AccountEmail string
	Phone        *string
	Fullname     string
	Address      *string
	AvatarURL    *string
	Bio          *string
	Locale       string
	Timezone     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
