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
	PasswordHash string
	Status       UserStatus
}

type LoginRequest struct {
	Username        string
	Password        string
	DevicePublicKey string
	TrustDevice     bool
	DeviceName      string
	ClientDeviceID  uuid.UUID
	ZoneCode        string
}

// VerifySessionResult chứa thông tin phản hồi sau khi xác thực Trinity session thành công
type VerifySessionResult struct {
	Valid  bool
	UserID string
	Role   string
	ZoneID string
}

// VerifyOpaqueRefreshTokenResult chứa thông tin phản hồi sau khi xác thực Opaque Refresh Token thành công
type VerifyOpaqueRefreshTokenResult struct {
	Valid    bool
	UserID   string
	TenantID string
	Role     string
	Level    int32
}

// VerifyUserCredentialsResult chứa thông tin phản hồi sau khi xác thực credentials người dùng thành công
type VerifyUserCredentialsResult struct {
	Valid          bool
	UserID         string
	Role           string
	Level          int32
	TenantID       string
	ClientDeviceID string
	RefreshToken   string
}

