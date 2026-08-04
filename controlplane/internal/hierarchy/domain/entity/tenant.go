package hierarchyEntity

import (
	"time"

	"github.com/google/uuid"
)

type TenantStatus string

const (
	TenantStatusActive    TenantStatus = "active"
	TenantStatusSuspended TenantStatus = "suspended"
	TenantStatusDeleted   TenantStatus = "deleted"
)

type CreateTenant struct {
	ID                uuid.UUID
	OwnerID           uuid.UUID
	OwnerMembershipID uuid.UUID
	TenantRootRoleID  uuid.UUID
	MembershipRoleID  uuid.UUID
	DomainID          uuid.UUID
	Code              string
	Name              string
	PrimaryDomain     string
	Status            TenantStatus
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
