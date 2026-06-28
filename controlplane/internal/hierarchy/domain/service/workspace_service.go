package zoneSvcInterface

import (
	"context"
	coreEntity "controlplane/internal/hierarchy/domain/entity"
)

// [COMMENT]: WorkspaceService định nghĩa giao diện nghiệp vụ quản lý Workspace
type WorkspaceService interface {
	// CreateWorkspace tạo workspace mới, đo metrics và ủy quyền cho repo insert
	CreateWorkspace(ctx context.Context, workspace coreEntity.Workspace) (*coreEntity.Workspace, error)
}
