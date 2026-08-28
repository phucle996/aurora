package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

// ListTenantRoles is isolated from platform Role so tenant ownership can never
// be inferred from a generic scope string.
type ListTenantRoles struct {
	ActorUserID              uuid.UUID
	TenantID                 uuid.UUID
	ID                       uuid.UUID
	Code                     string
	Name                     string
	Description              string
	RoleLevel                int
	Version                  int64
	AssignmentsCount         int
	OutdatedAssignmentsCount int
	PermissionsCount         int
	CreatedAt                time.Time
}

type CreateTenantRole struct {
	ID            uuid.UUID
	RevisionID    uuid.UUID
	ActorUserID   uuid.UUID
	TenantID      uuid.UUID
	Code          string
	Name          string
	Description   string
	RoleLevel     int
	Version       int64
	PermissionIDs []uuid.UUID
	CreatedAt     time.Time
}

type TenantRolePermission struct {
	ID          uuid.UUID
	Module      string
	Object      string
	Behavior    string
	Description string
}

type GetTenantRole struct {
	ActorUserID              uuid.UUID
	TenantID                 uuid.UUID
	ID                       uuid.UUID
	Code                     string
	Name                     string
	Description              string
	RoleLevel                int
	Version                  int64
	AssignmentsCount         int
	OutdatedAssignmentsCount int
	Permissions              []TenantRolePermission
	CreatedAt                time.Time
}

type CreateTenantRoleRevision struct {
	RevisionID      uuid.UUID
	ActorUserID     uuid.UUID
	TenantID        uuid.UUID
	RoleID          uuid.UUID
	ExpectedVersion int64
	Name            string
	Description     string
	RoleLevel       int
	Version         int64
	PermissionIDs   []uuid.UUID
	CreatedAt       time.Time
}

type UpgradeTenantRoleAssignments struct {
	ActorUserID  uuid.UUID
	TenantID     uuid.UUID
	RoleID       uuid.UUID
	Version      int64
	UpdatedCount int
}

type ResolveTenantAccess struct {
	UserID       uuid.UUID
	TenantID     uuid.UUID
	TenantDomain string
	RoleLevel    int32
}
