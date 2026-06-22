package iamRepoInterface

import (
	"context"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

type RbacRepository interface {
	// GetRoleByCode retrieves a role and its associated permissions by the role code.
	GetRoleByCode(ctx context.Context, code string) (*iamEntity.RoleWithPermissions, error)

	// GetPermissionCodesByRoleCode retrieves only the permission codes associated with a role code.
	// Used for lightweight lazy-loaded L1/L2 cache fallback.
	GetPermissionCodesByRoleCode(ctx context.Context, roleCode string) ([]string, error)

	// ListSystemRoleEntries retrieves all system/protected roles and their permissions for startup warming up.
	ListSystemRoleEntries(ctx context.Context) ([]*iamEntity.RoleWithPermissions, error)

	ListRoles(ctx context.Context) ([]*iamEntity.Role, error)
	GetRoleByID(ctx context.Context, id uuid.UUID) (*iamEntity.Role, error)
	CreateRole(ctx context.Context, role *iamEntity.Role) error
	UpdateRole(ctx context.Context, role *iamEntity.Role) error
	DeleteRole(ctx context.Context, id uuid.UUID) error

	ListPermissions(ctx context.Context) ([]*iamEntity.Permission, error)
	GetPermissionByID(ctx context.Context, id uuid.UUID) (*iamEntity.Permission, error)
	GetPermissionByCode(ctx context.Context, code string) (*iamEntity.Permission, error)
	AssignPermission(ctx context.Context, roleID, permissionID uuid.UUID) error
	RevokePermission(ctx context.Context, roleID, permissionID uuid.UUID) error

	AssignUserRole(ctx context.Context, userID, roleID uuid.UUID, scopeType iamEntity.RoleScopeType, tenantID, workspaceID *uuid.UUID, expiresAt *time.Time) error
	RevokeUserRole(ctx context.Context, userID, roleID uuid.UUID) error

	GetUserMaxRoleLevel(ctx context.Context, userID uuid.UUID) (int, error)
	GetUserRoleAndLevelByScope(ctx context.Context, userID uuid.UUID, scope string) (string, int, error)
}
