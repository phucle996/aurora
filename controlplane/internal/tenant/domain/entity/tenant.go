package tenantEntity

import "time"

type TenantStatus string

type MembershipStatus string

const (
	TenantStatusActive    TenantStatus = "active"
	TenantStatusSuspended TenantStatus = "suspended"
	TenantStatusDeleted   TenantStatus = "deleted"
)

const (
	MembershipStatusInvited MembershipStatus = "invited"
	MembershipStatusActive  MembershipStatus = "active"
	MembershipStatusRevoked MembershipStatus = "revoked"
)

type Tenant struct {
	ID        string
	Name      string
	Status    TenantStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

type TenantDomain struct {
	ID        string
	TenantID  string
	Domain    string
	IsPrimary bool
	CreatedAt time.Time
}

type TenantMembership struct {
	ID        string
	TenantID  string
	UserID    string
	Status    MembershipStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

type TenantRole struct {
	ID          string
	TenantID    string
	Code        string
	Name        string
	Description *string
	CreatedAt   time.Time
}

type CreateTenantInput struct {
	Name      string
	Domain    string
	CreatorID string
}

type CreateTenantResult struct {
	TenantID string
	Domain   string
}

type LoginTenantContext struct {
	TenantID string
	UserID   string
	Roles    []string
}
