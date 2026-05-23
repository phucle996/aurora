package iamModel

import (
	"time"

	"github.com/google/uuid"

	"controlplane/internal/iam/domain/entity"
)

type Permission struct {
	ID          uuid.UUID `db:"id"`
	Code        string    `db:"code"`
	Name        string    `db:"name"`
	Description *string   `db:"description"`
	Resource    string    `db:"resource"`
	Action      string    `db:"action"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

func PermissionEntityToModel(input iamEntity.Permission) Permission {
	return Permission{
		ID:          input.ID,
		Code:        input.Code,
		Name:        input.Name,
		Description: input.Description,
		Resource:    input.Resource,
		Action:      input.Action,
		CreatedAt:   input.CreatedAt,
		UpdatedAt:   input.UpdatedAt,
	}
}
func PermissionModelToEntity(input Permission) iamEntity.Permission {
	return iamEntity.Permission{
		ID:          input.ID,
		Code:        input.Code,
		Name:        input.Name,
		Description: input.Description,
		Resource:    input.Resource,
		Action:      input.Action,
		CreatedAt:   input.CreatedAt,
		UpdatedAt:   input.UpdatedAt,
	}
}

type Role struct {
	ID          uuid.UUID `db:"id"`
	Code        string    `db:"code"`
	Name        string    `db:"name"`
	Description *string   `db:"description"`
	ScopeType   string    `db:"scope_type"`
	RoleLevel   int       `db:"role_level"`
	IsSystem    bool      `db:"is_system"`
	IsProtected bool      `db:"is_protected"`
	IsAssignable bool     `db:"is_assignable"`
	OwnerTenantID *uuid.UUID `db:"owner_tenant_id"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

func RoleEntityToModel(input iamEntity.Role) Role {
	return Role{
		ID:          input.ID,
		Code:        input.Code,
		Name:        input.Name,
		Description: input.Description,
		ScopeType:   string(input.ScopeType),
		RoleLevel:   input.RoleLevel,
		IsSystem:    input.IsSystem,
		IsProtected: input.IsProtected,
		IsAssignable: input.IsAssignable,
		OwnerTenantID: input.OwnerTenantID,
		CreatedAt:   input.CreatedAt,
		UpdatedAt:   input.UpdatedAt,
	}
}
func RoleModelToEntity(input Role) iamEntity.Role {
	return iamEntity.Role{
		ID:          input.ID,
		Code:        input.Code,
		Name:        input.Name,
		Description: input.Description,
		ScopeType:   iamEntity.RoleScopeType(input.ScopeType),
		RoleLevel:   input.RoleLevel,
		IsSystem:    input.IsSystem,
		IsProtected: input.IsProtected,
		IsAssignable: input.IsAssignable,
		OwnerTenantID: input.OwnerTenantID,
		CreatedAt:   input.CreatedAt,
		UpdatedAt:   input.UpdatedAt,
	}
}

type RolePermission struct {
	RoleID       uuid.UUID `db:"role_id"`
	PermissionID uuid.UUID `db:"permission_id"`
	CreatedAt    time.Time `db:"created_at"`
}

func RolePermissionEntityToModel(input iamEntity.RolePermission) RolePermission {
	return RolePermission{
		RoleID:       input.RoleID,
		PermissionID: input.PermissionID,
		CreatedAt:    input.CreatedAt,
	}
}
func RolePermissionModelToEntity(input RolePermission) iamEntity.RolePermission {
	return iamEntity.RolePermission{
		RoleID:       input.RoleID,
		PermissionID: input.PermissionID,
		CreatedAt:    input.CreatedAt,
	}
}

type UserRole struct {
	ID          uuid.UUID  `db:"id"`
	UserID      uuid.UUID  `db:"user_id"`
	RoleID      uuid.UUID  `db:"role_id"`
	ScopeType   string     `db:"scope_type"`
	TenantID    *uuid.UUID `db:"tenant_id"`
	WorkspaceID *uuid.UUID `db:"workspace_id"`
	AssignedBy  *uuid.UUID `db:"assigned_by"`
	AssignedAt  time.Time  `db:"assigned_at"`
	ExpiresAt   *time.Time `db:"expires_at"`
	RevokedAt   *time.Time `db:"revoked_at"`
}

func UserRoleEntityToModel(input iamEntity.UserRole) UserRole {
	return UserRole{
		ID:          input.ID,
		UserID:      input.UserID,
		RoleID:      input.RoleID,
		ScopeType:   string(input.ScopeType),
		TenantID:    input.TenantID,
		WorkspaceID: input.WorkspaceID,
		AssignedBy:  input.AssignedBy,
		AssignedAt:  input.AssignedAt,
		ExpiresAt:   input.ExpiresAt,
		RevokedAt:   input.RevokedAt,
	}
}
func UserRoleModelToEntity(input UserRole) iamEntity.UserRole {
	return iamEntity.UserRole{
		ID:          input.ID,
		UserID:      input.UserID,
		RoleID:      input.RoleID,
		ScopeType:   iamEntity.RoleScopeType(input.ScopeType),
		TenantID:    input.TenantID,
		WorkspaceID: input.WorkspaceID,
		AssignedBy:  input.AssignedBy,
		AssignedAt:  input.AssignedAt,
		ExpiresAt:   input.ExpiresAt,
		RevokedAt:   input.RevokedAt,
	}
}
