package entity

import (
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: PersonalWorkspace đại diện cho không gian làm việc cá nhân (Personal/Me)
type PersonalWorkspace struct {
	ID          uuid.UUID
	Name        string
	Code        string
	Description string
	ZoneID      uuid.UUID
	OwnerID     uuid.UUID // ID của người sở hữu / quản trị Workspace
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// [COMMENT]: TenantWorkspace đại diện cho không gian làm việc thuộc Doanh nghiệp (Tenant)
type TenantWorkspace struct {
	ID          uuid.UUID
	Name        string
	Code        string
	Description string
	ZoneID      uuid.UUID
	TenantID    uuid.UUID // ID của Tenant sở hữu Workspace (bắt buộc)
	OwnerID     uuid.UUID // ID của người tạo ra Workspace
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// [COMMENT]: WorkspaceCatalog đại diện cho thông tin workspace tối giản trong hot path catalog
type WorkspaceCatalog struct {
	ID   uuid.UUID
	Code string
	Name string
}

// [COMMENT]: WorkspacePersonalListItem đại diện cho thông tin workspace cá nhân trả về cho client
type WorkspacePersonalListItem struct {
	ID          uuid.UUID
	Name        string
	Code        string
	Description string
	CreatedAt   time.Time
}
