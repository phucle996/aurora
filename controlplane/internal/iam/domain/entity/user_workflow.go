package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

// ListUsers is the flat contract for the platform user-directory workflow.
// The same workflow entity carries the bounded query and one returned row.
type ListUsers struct {
	CallerLevel  uint8
	Limit        int
	Offset       int
	ID           uuid.UUID
	Username     string
	Email        string
	Status       string
	RoleLevel    int32
	RoleName     string
	MFAEnabled   bool
	DevicesCount int32
	Bio          string
	Fullname     string
	LastSeenIP   string
	LastSeenAt   *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// UpdateUserStatus is the flat contract for the platform status mutation.
type UpdateUserStatus struct {
	CallerLevel  uint8
	TargetUserID uuid.UUID
	Status       string
}

// GetMyProfile is the flat contract for reading the current user's profile.
type GetMyProfile struct {
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

// UpdateMyProfile is the flat contract for the current user's profile update.
// Empty optional strings are interpreted as clearing the corresponding value
// only after the handler has canonicalized the transport DTO.
type UpdateMyProfile struct {
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

// GetMySocialLinks is one flat row of the self social-link workflow.
// A repository returns one row per provider; the handler owns response shaping.
type GetMySocialLinks struct {
	UserID          uuid.UUID
	Provider        string
	State           string
	ProviderEmail   string
	EmailVerifiedAt *time.Time
	LastLoginAt     *time.Time
	LinkedAt        *time.Time
	RevokedAt       *time.Time
}

// LinkExternalIdentity is the flat, already-verified identity contract
// shared only by the social-link workflow's handler, service and repository.
type LinkExternalIdentity struct {
	OperationID     uuid.UUID
	UserID          uuid.UUID
	Provider        string
	ProviderSubject string
	ProviderEmail   string
	EmailVerifiedAt time.Time
	DisplayName     string
	AvatarURL       *string
}

// UnlinkMySocialLink is the flat contract for removing one self social link.
type UnlinkMySocialLink struct {
	OperationID uuid.UUID
	UserID      uuid.UUID
	Provider    string
}

// GetUserAuthMethods is one flat row of the platform auth-method audit
// workflow. A repository returns one row per provider.
type GetUserAuthMethods struct {
	CallerLevel     uint8
	UserID          uuid.UUID
	AccountEmail    string
	PasswordSet     bool
	Provider        string
	State           string
	ProviderEmail   string
	EmailVerifiedAt *time.Time
	LastLoginAt     *time.Time
	LinkedAt        *time.Time
	RevokedAt       *time.Time
}

// ResetUserPassword is the flat contract for the administrator password
// mutation. Service clears Password after hashing before repository handoff.
type ResetUserPassword struct {
	OperationID  uuid.UUID
	CallerLevel  uint8
	TargetUserID uuid.UUID
	Password     string
	PasswordHash string
}
