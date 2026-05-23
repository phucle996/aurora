package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

type UserStatus string

const (
	UserStatusPendingActive UserStatus = "pending-active"
	UserStatusActive        UserStatus = "active"
	UserStatusSuspended     UserStatus = "suspended"
	UserStatusDisabled      UserStatus = "disabled"
)

type User struct {
	ID           uuid.UUID
	Username     string
	Email        string
	Phone        *string
	PasswordHash string
	Status       UserStatus
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type PasswordHistory struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	PasswordHash string
	CreatedAt    time.Time
}

type LoginUser struct {
	ID           uuid.UUID
	Username     string
	Email        string
	Fullname     string
	PasswordHash string
	Status       UserStatus
}

type LoginRequest struct {
	Username        string
	Password        string
	DevicePublicKey string
	IP              *string
	UserAgent       *string
	HostnameHint    string
	HostnameAlias   string
	ClientDeviceID  string
}

type LoginResult struct {
	AccessToken              string
	RefreshToken             string
	RuntimeDeviceID          string
	DeviceSecret             string
	TrackingID               string
	TrackedDeviceID          string
	ClientDeviceID           string
	ClientDeviceIDProvenance string
	AccessExpiresAt          time.Time
	RefreshExpiresAt         time.Time
}
