package hierarchyEntity

import (
	"time"

	"github.com/google/uuid"
)

type CreateTenantInvitation struct {
	ID                   uuid.UUID
	TenantID             uuid.UUID
	InviterUserID        uuid.UUID
	TargetIdentifier     string
	TargetByEmail        bool
	TargetUserID         uuid.UUID
	TenantRoleID         uuid.UUID
	TenantRoleRevisionID uuid.UUID
	WorkspaceID          uuid.UUID
	RoleCode             string
	RoleName             string
	RoleLevel            int
	RoleVersion          int64
	Token                string
	TokenHash            []byte
	ExpiresAt            time.Time
	CreatedAt            time.Time
}

type PreviewTenantInvitation struct {
	UserID      uuid.UUID
	TokenHash   []byte
	TenantID    uuid.UUID
	TenantCode  string
	TenantName  string
	InviterName string
	RoleCode    string
	RoleName    string
	RoleLevel   int
	RoleVersion int64
	ExpiresAt   time.Time
}

type RevokeTenantInvitation struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	ActorUserID  uuid.UUID
	TargetUserID uuid.UUID
	TenantRoleID uuid.UUID
}

type JoinTenantInvitation struct {
	UserID           uuid.UUID
	TokenHash        []byte
	MembershipID     uuid.UUID
	MembershipRoleID uuid.UUID
	TenantID         uuid.UUID
	TenantCode       string
	TenantName       string
	TenantRoleID     uuid.UUID
	RoleCode         string
	RoleName         string
	RoleLevel        int
}
