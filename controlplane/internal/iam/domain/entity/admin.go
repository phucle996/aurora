package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

type AdminAPIKey struct {
	ID        uuid.UUID
	KeyHash   string
	CreatedBy string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type AdminBootstrapPayload struct {
	Actor              string
	KeyHash            string
	ExpiresAt          time.Time
	RecoveryCodeHashes []string
	GeneratedAt        time.Time
	SecretCiphertext   string
	RequestPath        string
	RequestMethod      string
}

type AdminLoginRequest struct {
	RawAPIKey       string
	MFAMethod       MFAType
	MFACode         string
	DevicePublicKey string
	DeviceName      string
	ClientDeviceID  uuid.UUID
	// ZoneCode là code của phân vùng (ví dụ: "global", "vn-hn-1")
	ZoneCode string
}

type AdminLoginResult struct {
	// AdminAPIToken là JWT token ngắn hạn (Mảnh 1 của cơ chế Fragment Token), chứa các claims và được lưu ở cookie admin_api_token
	AdminAPIToken string
	// AccessKey là mã định danh phiên làm việc (Mảnh 2), dùng làm khóa truy vấn phiên trong Redis cache (cookie access_key)
	AccessKey string
	// AccessSecret là mã bí mật của phiên (Mảnh 3), được băm SHA256 khi lưu tại Redis cache và lưu thô tại cookie access_secret
	AccessSecret string
	// ClientDeviceID là mã định danh duy nhất của thiết bị được liên kết cố định với phiên quản trị (Device Binding)
	ClientDeviceID uuid.UUID
	// ClientDeviceIDProvenance mô tả nguồn gốc xuất xứ của Client Device ID (ví dụ: từ cookie hoặc thiết bị mới) phục vụ audit log
	ClientDeviceIDProvenance string
	// ExpiresAt là mốc thời gian Unix khi phiên làm việc của Admin chính thức hết hạn
	ExpiresAt time.Time
}

type Admin2FASettings struct {
	ID               uuid.UUID
	SecretCiphertext string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type AdminDeviceBindingInput struct {
	ID                   uuid.UUID
	DeviceName           string
	DeviceType           *string
	OSName               *string
	BrowserName          *string
	PublicKey            string
	PublicKeyFingerprint string
	LastSeenIP           *string
	LastSeenUserAgent    *string
	LastSeenAt           *time.Time
	Now                  time.Time
}

type AdminDevice struct {
	ID                   uuid.UUID
	DeviceName           string
	DeviceType           *string
	OSName               *string
	BrowserName          *string
	PublicKey            string
	PublicKeyFingerprint string
	ClientDeviceID       *string
	QuarantinedAt        *time.Time
	RevokedAt            *time.Time
	LastSeenIP           *string
	LastSeenUserAgent    *string
	LastSeenAt           *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}
