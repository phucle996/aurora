package hierarchyEntity

import (
	"time"

	"github.com/google/uuid"
)

type CreatePersonalWorkspace struct {
	ID          uuid.UUID
	Name        string
	Code        string
	Description string
	ZoneID      uuid.UUID
	OwnerID     uuid.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ListPersonalWorkspaces struct {
	OwnerID     uuid.UUID
	ID          uuid.UUID
	Name        string
	Code        string
	Description string
	CreatedAt   time.Time
}

type ListPersonalWorkspaceCatalog struct {
	OwnerID uuid.UUID
	ZoneID  uuid.UUID
	ID      uuid.UUID
	Code    string
	Name    string
}

type DeletePersonalWorkspace struct {
	ID      uuid.UUID
	OwnerID uuid.UUID
}

type CreateTenantWorkspace struct {
	ID          uuid.UUID
	Name        string
	Code        string
	Description string
	ZoneID      uuid.UUID
	TenantID    uuid.UUID
	OwnerID     uuid.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ListTenantWorkspaces struct {
	TenantID            uuid.UUID
	RoleID              uuid.UUID
	AllWorkspaces       bool
	AllowedWorkspaceIDs []uuid.UUID
	ID                  uuid.UUID
	Name                string
	Code                string
	Description         string
	ZoneID              uuid.UUID
	OwnerID             uuid.UUID
	CreatedAt           time.Time
}

type ListTenantWorkspaceCatalog struct {
	TenantID            uuid.UUID
	ZoneID              uuid.UUID
	RoleID              uuid.UUID
	AllWorkspaces       bool
	AllowedWorkspaceIDs []uuid.UUID
	ID                  uuid.UUID
	Code                string
	Name                string
}

type DeleteTenantWorkspace struct {
	ID       uuid.UUID
	TenantID uuid.UUID
}
