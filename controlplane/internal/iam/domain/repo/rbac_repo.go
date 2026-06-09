package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

type RbacRepository interface {
	GetRoleByCode(ctx context.Context, code string) (*iamEntity.RoleWithPermissions, error)
	ListRoleEntries(ctx context.Context) ([]*iamEntity.RoleWithPermissions, error)

	ListRoles(ctx context.Context) ([]*iamEntity.Role, error)
	GetRoleByID(ctx context.Context, id string) (*iamEntity.Role, error)
	CreateRole(ctx context.Context, role *iamEntity.Role) error
	UpdateRole(ctx context.Context, role *iamEntity.Role) error
	DeleteRole(ctx context.Context, id uuid.UUID) error

	ListPermissions(ctx context.Context) ([]*iamEntity.Permission, error)
	GetPermissionByID(ctx context.Context, id string) (*iamEntity.Permission, error)
	GetPermissionByCode(ctx context.Context, code string) (*iamEntity.Permission, error)
	AssignPermission(ctx context.Context, roleID, permissionID uuid.UUID) error
	RevokePermission(ctx context.Context, roleID, permissionID uuid.UUID) error

	// AssignUserRole V1 hiện chỉ bind platform scope.
	// Tenant/workspace scoped assignment được triển khai ở phase tiếp theo.
	AssignUserRole(ctx context.Context, userID, roleID string) error
	RevokeUserRole(ctx context.Context, userID, roleID string) error
}
