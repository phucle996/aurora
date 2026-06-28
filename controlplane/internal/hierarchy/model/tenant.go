package coreModel

import (
	coreEntity "controlplane/internal/hierarchy/domain/entity"
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: Tenant đại diện cho cấu trúc database mapping của bảng tenants
type Tenant struct {
	ID        uuid.UUID `db:"id"`
	Code      string    `db:"code"`
	Name      string    `db:"name"`
	Status    string    `db:"status"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// [COMMENT]: TenantDomain đại diện cho cấu trúc database mapping của bảng tenant_domains
type TenantDomain struct {
	ID        uuid.UUID `db:"id"`
	TenantID  uuid.UUID `db:"tenant_id"`
	Domain    string    `db:"domain"`
	IsPrimary bool      `db:"is_primary"`
	CreatedAt time.Time `db:"created_at"`
}

// [COMMENT]: TenantMembership đại diện cho cấu trúc database mapping của bảng tenant_memberships
type TenantMembership struct {
	ID        uuid.UUID `db:"id"`
	TenantID  uuid.UUID `db:"tenant_id"`
	UserID    uuid.UUID `db:"user_id"`
	Status    string    `db:"status"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// [COMMENT]: TenantEntityToModel chuyển đổi domain entity Tenant sang DB model tương ứng
func TenantEntityToModel(e coreEntity.Tenant) Tenant {
	return Tenant{
		ID:        e.ID,
		Code:      e.Code,
		Name:      e.Name,
		Status:    string(e.Status),
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
}

// [COMMENT]: TenantModelToEntity chuyển đổi DB model Tenant sang domain entity tương ứng
func TenantModelToEntity(m Tenant) coreEntity.Tenant {
	return coreEntity.Tenant{
		ID:        m.ID,
		Code:      m.Code,
		Name:      m.Name,
		Status:    coreEntity.TenantStatus(m.Status),
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

// [COMMENT]: TenantDomainEntityToModel chuyển đổi domain entity TenantDomain sang DB model tương ứng
func TenantDomainEntityToModel(e coreEntity.TenantDomain) TenantDomain {
	return TenantDomain{
		ID:        e.ID,
		TenantID:  e.TenantID,
		Domain:    e.Domain,
		IsPrimary: e.IsPrimary,
		CreatedAt: e.CreatedAt,
	}
}

// [COMMENT]: TenantDomainModelToEntity chuyển đổi DB model TenantDomain sang domain entity tương ứng
func TenantDomainModelToEntity(m TenantDomain) coreEntity.TenantDomain {
	return coreEntity.TenantDomain{
		ID:        m.ID,
		TenantID:  m.TenantID,
		Domain:    m.Domain,
		IsPrimary: m.IsPrimary,
		CreatedAt: m.CreatedAt,
	}
}

// [COMMENT]: TenantMembershipEntityToModel chuyển đổi domain entity TenantMembership sang DB model tương ứng
func TenantMembershipEntityToModel(e coreEntity.TenantMembership) TenantMembership {
	return TenantMembership{
		ID:        e.ID,
		TenantID:  e.TenantID,
		UserID:    e.UserID,
		Status:    string(e.Status),
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
}

// [COMMENT]: TenantMembershipModelToEntity chuyển đổi DB model TenantMembership sang domain entity tương ứng
func TenantMembershipModelToEntity(m TenantMembership) coreEntity.TenantMembership {
	return coreEntity.TenantMembership{
		ID:        m.ID,
		TenantID:  m.TenantID,
		UserID:    m.UserID,
		Status:    coreEntity.MembershipStatus(m.Status),
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}
