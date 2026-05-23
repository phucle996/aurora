package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type RoleEntry struct {
	Permissions []string
}

type AuthorizeResult string

const (
	AuthorizeAllow AuthorizeResult = "allow"
	AuthorizeDeny  AuthorizeResult = "deny"
	AuthorizeError AuthorizeResult = "error"
)

type RbacService interface {
	Authorize(ctx context.Context, roleCode, permission string) (AuthorizeResult, error)
	LoadRole(ctx context.Context, role string) (RoleEntry, error)
	InvalidateRole(ctx context.Context, role string)
	InvalidateAll(ctx context.Context)
	WarmUp(ctx context.Context) error

	ListRoles(ctx context.Context) ([]*iamEntity.Role, error)
	GetRole(ctx context.Context, id string) (*iamEntity.RoleWithPermissions, error)
	CreateRole(ctx context.Context, role *iamEntity.Role) error
	UpdateRole(ctx context.Context, role *iamEntity.Role) error
	DeleteRole(ctx context.Context, id string) error

	ListPermissions(ctx context.Context) ([]*iamEntity.Permission, error)
	CreatePermission(ctx context.Context, perm *iamEntity.Permission) error
	AssignPermission(ctx context.Context, roleID, permID string) error
	RevokePermission(ctx context.Context, roleID, permID string) error

	AssignUserRole(ctx context.Context, userID, roleID string) error
	RevokeUserRole(ctx context.Context, userID, roleID string) error
}
