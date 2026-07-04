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
	ID        uuid.UUID
	Code      string
	Name      string
	RoleLevel int
	Scope     string
	CreatedAt time.Time
	UpdatedAt time.Time
}
