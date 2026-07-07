package coreEntity

import (
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: WorkspaceStatus định nghĩa các trạng thái hoạt động của Workspace
type WorkspaceStatus string

const (
	WorkspaceStatusActive    WorkspaceStatus = "active"
	WorkspaceStatusSuspended WorkspaceStatus = "suspended"
	WorkspaceStatusDeleted   WorkspaceStatus = "deleted"
)

// [COMMENT]: Workspace đại diện cho không gian làm việc (đơn vị chứa tài nguyên ảo hóa của khách hàng)
// Bắt buộc phải liên kết với một Zone, có thể thuộc về 1 Tenant (doanh nghiệp) hoặc Null (cá nhân)
type Workspace struct {
	ID        uuid.UUID
	Name      string
	Code      string
	Status    WorkspaceStatus
	ZoneID    uuid.UUID
	TenantID  *uuid.UUID // Con trỏ UUID cho phép giá trị NULL (workspace cá nhân)
	OwnerID   uuid.UUID  // ID của người sở hữu / quản trị Workspace
	CreatedAt time.Time
	UpdatedAt time.Time
}

// [COMMENT]: WorkspaceCatalog đại diện cho thông tin workspace tối giản trong hot path catalog
type WorkspaceCatalog struct {
	ID   uuid.UUID `json:"id"`
	Code string    `json:"code"`
	Name string    `json:"name"`
}
