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
	Status    WorkspaceStatus
	ZoneID    uuid.UUID
	TenantID  *uuid.UUID // Con trỏ UUID cho phép giá trị NULL (workspace cá nhân)
	OwnerID   uuid.UUID  // ID của người sở hữu / quản trị Workspace
	CreatedAt time.Time
	UpdatedAt time.Time
}

// [COMMENT]: CreateWorkspaceInput chứa dữ liệu đầu vào từ handler để tạo workspace mới
type CreateWorkspaceInput struct {
	Name     string     // Tên hiển thị của workspace
	ZoneID   uuid.UUID  // Zone bắt buộc workspace thuộc về
	TenantID *uuid.UUID // Tenant sở hữu, nil nếu workspace cá nhân
	OwnerID  uuid.UUID  // User sở hữu workspace
}
