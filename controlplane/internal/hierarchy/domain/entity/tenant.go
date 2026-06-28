package coreEntity

import (
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: TenantStatus định nghĩa các trạng thái hoạt động của một Tenant
type TenantStatus string

const (
	TenantStatusActive    TenantStatus = "active"
	TenantStatusSuspended TenantStatus = "suspended"
	TenantStatusDeleted   TenantStatus = "deleted"
)

// [COMMENT]: Tenant đại diện cho thực thể Tổ chức / Doanh nghiệp sử dụng dịch vụ
type Tenant struct {
	ID        uuid.UUID
	Code      string
	Name      string
	Status    TenantStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

// [COMMENT]: TenantDomain đại diện cho một tên miền được liên kết sở hữu bởi Tenant
type TenantDomain struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Domain    string
	IsPrimary bool
	CreatedAt time.Time
}

// [COMMENT]: MembershipStatus định nghĩa trạng thái thành viên của người dùng trong Tenant
type MembershipStatus string

const (
	MembershipStatusInvited MembershipStatus = "invited"
	MembershipStatusActive  MembershipStatus = "active"
	MembershipStatusRevoked MembershipStatus = "revoked"
)

// [COMMENT]: TenantMembership đại diện cho mối liên kết thành viên giữa User và Tenant
type TenantMembership struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	UserID    uuid.UUID
	Status    MembershipStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}
