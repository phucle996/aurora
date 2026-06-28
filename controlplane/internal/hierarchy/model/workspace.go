package coreModel

import (
	coreEntity "controlplane/internal/hierarchy/domain/entity"
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: Workspace đại diện cho cấu trúc database mapping của bảng workspaces
type Workspace struct {
	ID        uuid.UUID  `db:"id"`
	Name      string     `db:"name"`
	Code      string     `db:"code"`
	Status    string     `db:"status"`
	ZoneID    uuid.UUID  `db:"zone_id"`
	TenantID  *uuid.UUID `db:"tenant_id"` // NULL nếu là workspace cá nhân
	OwnerID   uuid.UUID  `db:"owner_id"`
	CreatedAt time.Time  `db:"created_at"`
	UpdatedAt time.Time  `db:"updated_at"`
}

// [COMMENT]: WorkspaceEntityToModel chuyển đổi domain entity Workspace sang DB model tương ứng
func WorkspaceEntityToModel(e coreEntity.Workspace) Workspace {
	return Workspace{
		ID:        e.ID,
		Name:      e.Name,
		Code:      e.Code,
		Status:    string(e.Status),
		ZoneID:    e.ZoneID,
		TenantID:  e.TenantID,
		OwnerID:   e.OwnerID,
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
}

// [COMMENT]: WorkspaceModelToEntity chuyển đổi DB model Workspace sang domain entity tương ứng
func WorkspaceModelToEntity(m Workspace) coreEntity.Workspace {
	return coreEntity.Workspace{
		ID:        m.ID,
		Name:      m.Name,
		Code:      m.Code,
		Status:    coreEntity.WorkspaceStatus(m.Status),
		ZoneID:    m.ZoneID,
		TenantID:  m.TenantID,
		OwnerID:   m.OwnerID,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}
