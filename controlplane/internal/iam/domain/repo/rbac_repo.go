package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type RbacRepository interface {
	GetRoleByCode(ctx context.Context, code string) (*iamEntity.RoleWithPermissions, error)
	ListRoleEntries(ctx context.Context) ([]*iamEntity.RoleWithPermissions, error)

	ListRoles(ctx context.Context) ([]*iamEntity.Role, error)
	GetRoleByID(ctx context.Context, id string) (*iamEntity.Role, error)
	CreateRole(ctx context.Context, role *iamEntity.Role) error
	UpdateRole(ctx context.Context, role *iamEntity.Role) error
	DeleteRole(ctx context.Context, id string) error

	ListPermissions(ctx context.Context) ([]*iamEntity.Permission, error)
	GetPermissionByID(ctx context.Context, id string) (*iamEntity.Permission, error)
	GetPermissionByCode(ctx context.Context, code string) (*iamEntity.Permission, error)
	CreatePermission(ctx context.Context, perm *iamEntity.Permission) error
	AssignPermission(ctx context.Context, roleID, permissionID string) error
	RevokePermission(ctx context.Context, roleID, permissionID string) error

	// AssignUserRole V1 hiện chỉ bind platform scope.
	// Tenant/workspace scoped assignment được triển khai ở phase tiếp theo.
	AssignUserRole(ctx context.Context, userID, roleID string) error
	RevokeUserRole(ctx context.Context, userID, roleID string) error
}
