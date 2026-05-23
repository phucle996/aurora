package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

type UserProfile struct {
	UserID    uuid.UUID
	Fullname  string
	AvatarURL *string
	Bio       *string
	Locale    string
	Timezone  string
	CreatedAt time.Time
	UpdatedAt time.Time
}
