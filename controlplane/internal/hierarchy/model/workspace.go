package model

import (
	entity "controlplane/internal/hierarchy/domain/entity"
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: PersonalWorkspace đại diện cho cấu trúc database mapping của bảng personal_workspaces
type PersonalWorkspace struct {
	ID          uuid.UUID `db:"id"`
	Name        string    `db:"name"`
	Code        string    `db:"code"`
	Description string    `db:"description"`
	ZoneID      uuid.UUID `db:"zone_id"`
	OwnerID     uuid.UUID `db:"owner_id"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

// [COMMENT]: PersonalWorkspaceEntityToModel chuyển đổi domain entity PersonalWorkspace sang DB model tương ứng
func PersonalWorkspaceEntityToModel(e entity.PersonalWorkspace) PersonalWorkspace {
	return PersonalWorkspace{
		ID:          e.ID,
		Name:        e.Name,
		Code:        e.Code,
		Description: e.Description,
		ZoneID:      e.ZoneID,
		OwnerID:     e.OwnerID,
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
}

// [COMMENT]: PersonalWorkspaceModelToEntity chuyển đổi DB model PersonalWorkspace sang domain entity tương ứng
func PersonalWorkspaceModelToEntity(m PersonalWorkspace) entity.PersonalWorkspace {
	return entity.PersonalWorkspace{
		ID:          m.ID,
		Name:        m.Name,
		Code:        m.Code,
		Description: m.Description,
		ZoneID:      m.ZoneID,
		OwnerID:     m.OwnerID,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

// [COMMENT]: TenantWorkspace đại diện cho cấu trúc database mapping của bảng tenant_workspaces
type TenantWorkspace struct {
	ID          uuid.UUID `db:"id"`
	Name        string    `db:"name"`
	Code        string    `db:"code"`
	Description string    `db:"description"`
	ZoneID      uuid.UUID `db:"zone_id"`
	TenantID    uuid.UUID `db:"tenant_id"` // Bắt buộc NOT NULL
	OwnerID     uuid.UUID `db:"owner_id"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

// [COMMENT]: TenantWorkspaceEntityToModel chuyển đổi domain entity TenantWorkspace sang DB model tương ứng
func TenantWorkspaceEntityToModel(e entity.TenantWorkspace) TenantWorkspace {
	return TenantWorkspace{
		ID:          e.ID,
		Name:        e.Name,
		Code:        e.Code,
		Description: e.Description,
		ZoneID:      e.ZoneID,
		TenantID:    e.TenantID,
		OwnerID:     e.OwnerID,
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
}

// [COMMENT]: TenantWorkspaceModelToEntity chuyển đổi DB model TenantWorkspace sang domain entity tương ứng
func TenantWorkspaceModelToEntity(m TenantWorkspace) entity.TenantWorkspace {
	return entity.TenantWorkspace{
		ID:          m.ID,
		Name:        m.Name,
		Code:        m.Code,
		Description: m.Description,
		ZoneID:      m.ZoneID,
		TenantID:    m.TenantID,
		OwnerID:     m.OwnerID,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}
