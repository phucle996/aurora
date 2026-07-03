package iamSvcInterface

import (
	"context"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// ============================================================================
// 🛡️ CƠ CHẾ HOẠT ĐỘNG CỦA HỆ THỐNG PHÂN QUYỀN HIERARCHICAL RBAC
// ============================================================================
//
// 1. PHÂN CẤP QUYỀN HẠN (ROLE LEVEL HIERARCHY):
//    - Vai trò (Role) được định nghĩa kèm theo chỉ số `role_level` (số nguyên >= 0).
//    - Giá trị `role_level` càng nhỏ thì quyền lực càng lớn. Level 0 là Super Admin/Platform Admin.
//    - Nguyên tắc chống leo thang (Privilege Escalation Prevention): Một Actor có cấp bậc L_actor
//      chỉ được phép thực hiện hành động trên các vai trò có cấp bậc L_target sao cho L_target > L_actor
//      (tức là Actor có quyền lực cao hơn đối tượng bị tác động về mặt số học).
//
// 2. PHÂN HOẠCH PHẠM VI (ROLE SCOPING):
//    - Platform ('platform'): Quyền hạn toàn hệ thống. Cả TenantID và WorkspaceID phải là NULL.
//    - Tenant ('tenant'): Quyền hạn trong một Tenant. TenantID bắt buộc có, WorkspaceID phải NULL.
//    - Workspace ('workspace'): Quyền hạn trong Workspace. Cả TenantID và WorkspaceID bắt buộc có.
//
// 3. QUY TẮC BẢO VỆ (ROLE PROTECTION):
//    - Các vai trò hệ thống (is_system = true hoặc is_protected = true) không thể bị chỉnh sửa,
//      cập nhật, hoặc xóa qua API nghiệp vụ thường để đảm bảo tính toàn vẹn hệ thống.
//
// 4. HIỆU NĂNG VÀ CẬP NHẬT CACHE (HA EVENTUAL CONSISTENCY):
//    - Các thao tác Authorize sử dụng L1 Cache (in-memory) kết hợp L2 Cache (Redis).
//    - Khi có hành động thay đổi quyền (Assign/Revoke role/permission), hệ thống sẽ xoá cache
//      tương ứng trên Redis L2 và phát tín hiệu Pub/Sub (Fanout) để các Pod runtime xoá cache L1 tức thì.
//
// ============================================================================

type RoleEntry struct {
	Permissions []string `json:"permissions"`
}

type AuthorizeResult string

const (
	AuthorizeAllow AuthorizeResult = "allow"
	AuthorizeDeny  AuthorizeResult = "deny"
	AuthorizeError AuthorizeResult = "error"
)

type RbacService interface {
	// ListRoles trả về danh sách các vai trò mà Actor hiện tại có quyền xem (role_level của vai trò > role_level của Actor).
	ListRoles(ctx context.Context, actorLevel int) ([]*iamEntity.Role, error)

	// GetRole chi tiết thông tin vai trò cùng danh sách quyền hạn đi kèm.
	GetRole(ctx context.Context, actorLevel int, id uuid.UUID) (*iamEntity.RoleWithPermissions, error)

	// CreateRole tạo vai trò mới (vai trò mới phải có role_level > role_level của Actor).
	CreateRole(ctx context.Context, actorLevel int, role *iamEntity.Role) error

	// UpdateRole cập nhật thông tin vai trò (chỉ vai trò do custom tạo ra và có role_level > role_level của Actor).
	UpdateRole(ctx context.Context, actorLevel int, role *iamEntity.Role) error

	// DeleteRole xóa vai trò khỏi hệ thống (chỉ vai trò custom và có role_level > role_level của Actor).
	DeleteRole(ctx context.Context, actorLevel int, id uuid.UUID) error

	// ListPermissions lấy toàn bộ danh sách các permission hiện có trong hệ thống.
	ListPermissions(ctx context.Context) ([]*iamEntity.Permission, error)

	// AssignPermission gán một permission cho một vai trò custom (yêu cầu role_level của vai trò > role_level của Actor).
	AssignPermission(ctx context.Context, actorLevel int, roleID, permID uuid.UUID) error

	// RevokePermission thu hồi permission khỏi vai trò custom (yêu cầu role_level của vai trò > role_level của Actor).
	RevokePermission(ctx context.Context, actorLevel int, roleID, permID uuid.UUID) error

	// AssignUserRole gán vai trò cho user với scope, thời hạn tương ứng (yêu cầu role_level của vai trò > role_level của Actor).
	AssignUserRole(ctx context.Context, actorLevel int, userID, roleID uuid.UUID, scopeType iamEntity.RoleScopeType, tenantID, workspaceID *uuid.UUID, expiresAt *time.Time) error

	// RevokeUserRole thu hồi vai trò khỏi user (yêu cầu role_level của vai trò > role_level của Actor).
	RevokeUserRole(ctx context.Context, actorLevel int, userID, roleID uuid.UUID) error
}

