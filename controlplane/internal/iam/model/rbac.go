package iamModel

import (
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: UserRole đại diện cho cấu trúc bảng user_role trong PostgreSQL
type UserRole struct {
	ID          uuid.UUID `db:"id"`
	UserID      uuid.UUID `db:"user_id"`
	Username    string    `db:"username"`
	WorkspaceID uuid.UUID `db:"workspace_id"`
	RoleID      uuid.UUID `db:"role_id"`
	RoleName    string    `db:"role_name"`
	RoleLevel   int       `db:"role_level"` // Level phân cấp của role
	ListPerm    []byte    `db:"list_perm"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

// [COMMENT]: UserRoleEntityToModel chuyển đổi thực thể domain sang DB model
func UserRoleEntityToModel(input iamEntity.UserRole) UserRole {
	return UserRole{
		ID:          input.ID,
		UserID:      input.UserID,
		Username:    input.Username,
		WorkspaceID: input.WorkspaceID,
		RoleID:      input.RoleID,
		RoleName:    input.RoleName,
		RoleLevel:   input.RoleLevel,
		ListPerm:    input.ListPerm,
		CreatedAt:   input.CreatedAt,
		UpdatedAt:   input.UpdatedAt,
	}
}

// [COMMENT]: UserRoleModelToEntity chuyển đổi DB model sang thực thể domain
func UserRoleModelToEntity(input UserRole) iamEntity.UserRole {
	return iamEntity.UserRole{
		ID:          input.ID,
		UserID:      input.UserID,
		Username:    input.Username,
		WorkspaceID: input.WorkspaceID,
		RoleID:      input.RoleID,
		RoleName:    input.RoleName,
		RoleLevel:   input.RoleLevel,
		ListPerm:    input.ListPerm,
		CreatedAt:   input.CreatedAt,
		UpdatedAt:   input.UpdatedAt,
	}
}

// [COMMENT]: TenantRole đại diện cho cấu trúc bảng tenant_role trong PostgreSQL
type TenantRole struct {
	ID          uuid.UUID `db:"id"`
	TenantID    uuid.UUID `db:"tenant_id"`
	WorkspaceID uuid.UUID `db:"workspace_id"`
	RoleID      uuid.UUID `db:"role_id"`
	RoleName    string    `db:"role_name"`
	RoleLevel   int       `db:"role_level"` // Level phân cấp của role
	ListPerm    []byte    `db:"list_perm"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

// [COMMENT]: TenantRoleEntityToModel chuyển đổi thực thể domain sang DB model
func TenantRoleEntityToModel(input iamEntity.TenantRole) TenantRole {
	return TenantRole{
		ID:          input.ID,
		TenantID:    input.TenantID,
		WorkspaceID: input.WorkspaceID,
		RoleID:      input.RoleID,
		RoleName:    input.RoleName,
		RoleLevel:   input.RoleLevel,
		ListPerm:    input.ListPerm,
		CreatedAt:   input.CreatedAt,
		UpdatedAt:   input.UpdatedAt,
	}
}

// [COMMENT]: TenantRoleModelToEntity chuyển đổi DB model sang thực thể domain
func TenantRoleModelToEntity(input TenantRole) iamEntity.TenantRole {
	return iamEntity.TenantRole{
		ID:          input.ID,
		TenantID:    input.TenantID,
		WorkspaceID: input.WorkspaceID,
		RoleID:      input.RoleID,
		RoleName:    input.RoleName,
		RoleLevel:   input.RoleLevel,
		ListPerm:    input.ListPerm,
		CreatedAt:   input.CreatedAt,
		UpdatedAt:   input.UpdatedAt,
	}
}

// [COMMENT]: Role đại diện cho bảng roles trong PostgreSQL (ánh xạ đúng các cột của bảng roles)
type Role struct {
	ID          uuid.UUID `db:"id"`
	Code        string    `db:"code"`
	Name        string    `db:"name"`
	Description string    `db:"description"`
	RoleLevel   int       `db:"role_level"`
	Scope       string    `db:"scope"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

// [COMMENT]: RoleModelToEntity chuyển đổi DB model sang thực thể domain
func RoleModelToEntity(input Role) iamEntity.Role {
	return iamEntity.Role{
		ID:          input.ID,
		Code:        input.Code,
		Name:        input.Name,
		Description: input.Description,
		RoleLevel:   input.RoleLevel,
		Scope:       input.Scope,
		CreatedAt:   input.CreatedAt,
		UpdatedAt:   input.UpdatedAt,
	}
}

// [COMMENT]: Permission đại diện cho cấu trúc bảng permissions trong PostgreSQL
type Permission struct {
	ID          uuid.UUID `db:"id"`
	Module      string    `db:"module"`
	Object      string    `db:"object"`
	Behavior    string    `db:"behavior"`
	Description string    `db:"description"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

// [COMMENT]: PermissionModelToEntity chuyển đổi DB model sang thực thể domain
func PermissionModelToEntity(input Permission) iamEntity.Permission {
	return iamEntity.Permission{
		ID:          input.ID,
		Module:      input.Module,
		Object:      input.Object,
		Behavior:    input.Behavior,
		Description: input.Description,
		CreatedAt:   input.CreatedAt,
		UpdatedAt:   input.UpdatedAt,
	}
}
