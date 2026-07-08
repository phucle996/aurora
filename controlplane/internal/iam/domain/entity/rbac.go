package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: UserRole đại diện cho thực thể phân vai trò của User trong Workspace với permissions dạng binary (bytea)
type UserRole struct {
	ID          uuid.UUID // ID của mapping record
	UserID      uuid.UUID // ID của người dùng được gán role (FK)
	Username    string    // Tên đăng nhập canonical của người dùng (cached)
	WorkspaceID uuid.UUID // ID workspace áp dụng role (nil UUID đại diện platform scope)
	RoleID      uuid.UUID // ID role tĩnh từ Go static config
	RoleName    string    // Tên hiển thị của role (cached)
	RoleLevel   int       // Level phân cấp của role (cached)
	ListPerm    []byte    // Mảng binary chứa danh sách keys 5 cấp đã serialize (Protobuf RoleEntry)
	CreatedAt   time.Time // Thời điểm tạo
	UpdatedAt   time.Time // Thời điểm cập nhật
}

// [COMMENT]: TenantRole đại diện cho thực thể phân vai trò của Tenant trong Workspace với permissions dạng binary (bytea)
type TenantRole struct {
	ID          uuid.UUID // ID của mapping record
	TenantID    uuid.UUID // ID của Tenant được gán role (FK)
	WorkspaceID uuid.UUID // ID workspace áp dụng role (nil UUID đại diện platform scope)
	RoleID      uuid.UUID // ID role tĩnh từ Go static config
	RoleName    string    // Tên hiển thị của role (cached)
	RoleLevel   int       // Level phân cấp của role (cached)
	ListPerm    []byte    // Mảng binary chứa danh sách keys 5 cấp đã serialize (Protobuf RoleEntry)
	CreatedAt   time.Time // Thời điểm tạo
	UpdatedAt   time.Time // Thời điểm cập nhật
}

// [COMMENT]: Role đại diện cho định nghĩa vai trò hệ thống (Platform/Tenant scope)
type Role struct {
	ID          uuid.UUID
	Code        string
	Name        string
	Description string
	RoleLevel   int
	Scope       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// [COMMENT]: NavigationItem định nghĩa cấu trúc menu gom nhóm 4 cấp kèm theo các hành động (behavior/action) được phép
type NavigationItem struct {
	Key     string     // format: <scope>:<workspace_uuid>:<module>:<object>
	Actions []string   // các action (behavior) tương ứng, ví dụ ["list", "delete"] hoặc ["*"]
}

// [COMMENT]: RenderContext bọc danh sách menu và capabilities tương ứng của Actor
type RenderContext struct {
	Navigation   []NavigationItem
	Capabilities map[string]bool
}

// [COMMENT]: Permission đại diện cho một quyền hạn hệ thống chi tiết
type Permission struct {
	ID          uuid.UUID
	Module      string
	Object      string
	Behavior    string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

