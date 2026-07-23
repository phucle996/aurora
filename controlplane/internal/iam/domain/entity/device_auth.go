package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

type ChallengeStatus string

const (
	ChallengeStatusPending  ChallengeStatus = "pending"
	ChallengeStatusVerified ChallengeStatus = "verified"
	ChallengeStatusExpired  ChallengeStatus = "expired"
	ChallengeStatusFailed   ChallengeStatus = "failed"
	ChallengeStatusConsumed ChallengeStatus = "consumed"
)

// [COMMENT]: Device đại diện cho thông tin thiết bị đã đăng ký của user
type Device struct {
	ID                   string
	UserID               uuid.UUID
	DeviceName           string
	DeviceType           *string
	OSName               *string
	BrowserName          *string
	PublicKey            string
	PublicKeyFingerprint string
	ClientDeviceID       *string
	RiskFlags            map[string]any
	RevokedAt            *time.Time
	LastSeenIP           *string
	LastSeenUserAgent    *string
	LastSeenAt           *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// [COMMENT]: DevicePresence đại diện cho một thiết bị thực tế đang hoạt động kèm mốc thời gian/trạng thái cuối
type DevicePresence struct {
	ID         string
	DeviceName string
	IsOnline   bool
	LastSeenAt *time.Time
	LastIP     *string
	LastUA     *string
	RevokedAt  *time.Time
}

type DeviceListResult struct {
	Devices []DevicePresence
	Total   int64
}

// [COMMENT]: DevicePresenceUpdate chứa thông tin cập nhật heartbeat cho một thiết bị,
// ánh xạ từ Protobuf BulkTouchDevicesRequest.DeviceUpdate gửi qua Shared Redis từ ACR
type DevicePresenceUpdate struct {
	DeviceID          string // client_device_id (UUID dạng string)
	LastSeenAt        int64  // Unix timestamp (giây)
	LastSeenIP        string
	LastSeenUserAgent string
}
