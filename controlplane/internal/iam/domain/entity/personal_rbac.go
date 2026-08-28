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
	RoleVersion int64
	ListPerm    []byte    // Mảng binary chứa danh sách keys 5 cấp đã serialize (Protobuf RoleEntry)
	CreatedAt   time.Time // Thời điểm tạo
	UpdatedAt   time.Time // Thời điểm cập nhật
}

// [COMMENT]: Role đại diện cho định nghĩa vai trò hệ thống, bổ sung các trường thống kê phục vụ hiển thị danh sách (Platform scope)
type Role struct {
	ID               uuid.UUID
	Code             string
	Name             string
	Description      string
	RoleLevel        int
	Scope            string
	CreatedBy        uuid.UUID // ID người dùng tạo vai trò này
	CreatedByName    string    // Tên đầy đủ (fullname) người dùng tạo vai trò này
	AssignmentsCount int
	PermissionsCount int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// [COMMENT]: UpdateRoleInput đại diện cho thực thể đầu vào phục vụ cập nhật vai trò platform
type UpdateRoleInput struct {
	ID            uuid.UUID
	Name          string
	Description   string
	PermissionIDs []uuid.UUID
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
