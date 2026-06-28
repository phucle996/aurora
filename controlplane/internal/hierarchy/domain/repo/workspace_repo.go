package coreRepoInterface

import (
	"context"
	coreEntity "controlplane/internal/hierarchy/domain/entity"
)

// [COMMENT]: WorkspaceRepository định nghĩa giao diện truy cập dữ liệu cho Workspace
type WorkspaceRepository interface {
	// CreateWorkspace tạo workspace mới với ràng buộc zone phải tồn tại và active,
	// tenant (nếu có) phải tồn tại và active. Trả về workspace đã tạo hoặc lỗi cụ thể.
	CreateWorkspace(ctx context.Context, workspace coreEntity.Workspace) (*coreEntity.Workspace, error)
}
