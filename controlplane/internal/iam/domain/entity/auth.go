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
	Level        int32      // [COMMENT]: Level thô (chỉ dùng nội bộ ở Service/Repo, ẩn khi trả về Client)
	RoleName     string     // [COMMENT]: Tên hiển thị platform role được gán của user
	MfaEnabled   bool       // [COMMENT]: Cờ cho biết user đã kích hoạt xác thực hai lớp (MFA) hay chưa
	DevicesCount int32      // [COMMENT]: Số lượng thiết bị đã đăng ký của user
	Bio          string     // [COMMENT]: Mô tả ngắn về user lấy từ user_profiles
	Fullname     string     // [COMMENT]: Tên hiển thị đầy đủ lấy từ user_profiles
	LastSeenIP   string     // [COMMENT]: IP gần nhất được ghi nhận từ device hoạt động cuối cùng
	LastSeenAt   *time.Time // [COMMENT]: Thời điểm hoạt động gần nhất qua thiết bị
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
	PasswordHash *string
	Status       UserStatus
	// [COMMENT]: TenantID và TenantCode phục vụ flow login username@tenant_domain.
	// Rỗng/nil nếu login global.
	TenantID *string
	// [COMMENT]: RoleID (UUID) và Level được join trực tiếp từ RBAC trong repo
	RoleID string
	Level  int32
}

type LoginRequest struct {
	Username        string
	Password        string
	DevicePublicKey string
	TrustDevice     bool
	DeviceName      string
	ClientDeviceID  uuid.UUID
	// [COMMENT]: TenantDomain được ACR tách từ username@tenant_domain trước khi gọi gRPC sang CP.
	// Rỗng = đăng nhập global (platform scope), có giá trị = lấy tenant context qua JOIN tenant_domains.
	TenantDomain string
	RemoteIP     string
	UserAgent    string
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
	RoleID   string
	Level    int32
	Username string
}

// VerifyUserCredentialsResult chứa thông tin phản hồi sau khi xác thực credentials người dùng thành công
type VerifyUserCredentialsResult struct {
	Valid        bool
	MFARequired  bool
	UserID       string
	MFASettingID string
	// [COMMENT]: RoleID là UUID của role đang hoạt động, ACR sẽ inject vào JWT claims và forward qua header X-User-Role-ID
	RoleID                string
	Level                 int32
	TenantID              string
	ClientDeviceID        string
	RefreshToken          string
	RefreshTokenExpiresAt time.Time
	Username              string
	ClientProofPublicKey  string
	// [COMMENT]: TenantCode được điền khi login qua tenant_domain. Rỗng nếu login global.
	TenantCode string
}

type ExternalProvider string

const (
	ExternalProviderGoogle ExternalProvider = "google"
	ExternalProviderGitHub ExternalProvider = "github"
)

type ExternalIdentity struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	Provider        ExternalProvider
	ProviderSubject string
	ProviderEmail   string
	EmailVerifiedAt time.Time
	DisplayName     string
	AvatarURL       *string
	LastLoginAt     *time.Time
	RevokedAt       *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// VerifiedExternalIdentity is the only identity shape accepted from ACR.
// Provider JSON, JWT claims and authorization codes must never cross this boundary.
type VerifiedExternalIdentity struct {
	Provider        ExternalProvider
	Subject         string
	Email           string
	EmailVerifiedAt time.Time
	DisplayName     string
	AvatarURL       *string
}

type ExternalLoginRequest struct {
	OperationID     uuid.UUID
	Identity        VerifiedExternalIdentity
	DevicePublicKey string
	TrustDevice     bool
	DeviceName      string
	DeviceType      string
	ClientDeviceID  uuid.UUID
	ZoneCode        string
	RemoteIP        string
	UserAgent       string
}

type ExternalLoginResult struct {
	Valid                 bool
	MFARequired           bool
	UserID                string
	MFASettingID          string
	RoleID                string
	Level                 int32
	TenantID              string
	ClientDeviceID        string
	RefreshToken          string
	RefreshTokenExpiresAt time.Time
	Username              string
	ClientProofPublicKey  string
	ZoneCode              string
}
