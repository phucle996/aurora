package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

type RoleScopeType string

const (
	RoleScopeTypePlatform  RoleScopeType = "platform"
	RoleScopeTypeTenant    RoleScopeType = "tenant"
	RoleScopeTypeWorkspace RoleScopeType = "workspace"
)

type Permission struct {
	ID          uuid.UUID
	Code        string
	Name        string
	Description *string
	Resource    string
	Action      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Role struct {
	ID            uuid.UUID
	Code          string
	Name          string
	Description   *string
	ScopeType     RoleScopeType
	RoleLevel     int
	IsSystem      bool
	IsProtected   bool
	IsAssignable  bool
	OwnerTenantID *uuid.UUID
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type RolePermission struct {
	RoleID       uuid.UUID
	PermissionID uuid.UUID
	CreatedAt    time.Time
}

type UserRole struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	RoleID      uuid.UUID
	ScopeType   RoleScopeType
	TenantID    *uuid.UUID
	WorkspaceID *uuid.UUID
	AssignedBy  *uuid.UUID
	AssignedAt  time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
}

type RoleWithPermissions struct {
	Role        *Role
	Permissions []string
}
