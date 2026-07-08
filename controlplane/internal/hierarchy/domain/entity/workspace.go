package coreEntity

import (
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: Workspace đại diện cho không gian làm việc (đơn vị chứa tài nguyên ảo hóa của khách hàng)
// Bắt buộc phải liên kết với một Zone, có thể thuộc về 1 Tenant (doanh nghiệp) hoặc Null (cá nhân)
type Workspace struct {
	ID          uuid.UUID
	Name        string
	Code        string
	Description string
	ZoneID      uuid.UUID
	TenantID    *uuid.UUID // Con trỏ UUID cho phép giá trị NULL (workspace cá nhân)
	OwnerID     uuid.UUID  // ID của người sở hữu / quản trị Workspace
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
