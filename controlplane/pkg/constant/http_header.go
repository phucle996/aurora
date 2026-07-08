package constant

// Định nghĩa các HTTP Header dùng để truyền danh tính từ Edge Gateway (ACR) tới Upstream Services.
const (
	HeaderXUserID     = "X-User-ID"
	HeaderXUserName   = "X-User-Name"
	HeaderXDeviceID   = "X-Device-ID"
	HeaderXTenantID   = "X-Tenant-ID"
	HeaderXTenantCode = "X-Tenant-Code"
	HeaderXZoneID     = "X-Zone-ID"
	// HeaderXUserRole chứa role code của user — dùng cho root bypass ở middleware
	HeaderXUserRole = "X-User-Role"
	// HeaderXUserRoleID chứa UUID của role đang hoạt động do ACR inject từ JWT claims
	HeaderXUserRoleID = "X-User-Role-ID"
	// HeaderXWorkspaceID chứa UUID của workspace đang hoạt động do ACR inject từ cookie client
	HeaderXWorkspaceID = "X-Workspace-ID"
	HeaderXUserLevel   = "X-User-Level"
	HeaderTraceparent  = "traceparent"
	HeaderXRequestID   = "X-Request-ID"
)

// Context Keys dùng để truyền danh tính trong Go Context giữa tầng HTTP Handler và Service/Repo
type ContextKeyUserRoleType struct{}
type ContextKeyUserLevelType struct{}
type ContextKeyTenantIDType struct{}
type ContextKeyZoneIDType struct{}
type ContextKeyDeviceIDType struct{}

var (
	ContextKeyUserRole  = ContextKeyUserRoleType{}
	ContextKeyUserLevel = ContextKeyUserLevelType{}
	ContextKeyTenantID  = ContextKeyTenantIDType{}
	ContextKeyZoneID    = ContextKeyZoneIDType{}
	ContextKeyDeviceID  = ContextKeyDeviceIDType{}
)

// Context Keys cho Remote IP và User Agent
type RemoteIPKeyType struct{}
type UserAgentKeyType struct{}

var (
	RemoteIPKey  = RemoteIPKeyType{}
	UserAgentKey = UserAgentKeyType{}
)
