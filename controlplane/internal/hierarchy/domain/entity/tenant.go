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

// TenantCatalogItem is the verified user's personal tenant switch catalog.
// The catalog is intentionally read-only and contains only durable tenant
// identity needed by the ACR switch workflow.
type TenantCatalogItem struct {
	ID            uuid.UUID
	Code          string
	Name          string
	PrimaryDomain string
	RoleName      string
	RoleLevel     int
}

// TenantWalletProvisionOutbox is the flat durable command claimed only by the
// tenant wallet-provision relay. It is not a cross-workflow outbox entity.
type TenantWalletProvisionOutbox struct {
	ID          int64
	EventID     uuid.UUID
	TenantID    uuid.UUID
	ActorUserID uuid.UUID
	Payload     []byte
}
