package zoneSvcInterface

import (
	"context"
	coreEntity "controlplane/internal/hierarchy/domain/entity"
)

// [COMMENT]: WorkspaceService định nghĩa giao diện nghiệp vụ quản lý Workspace
type WorkspaceService interface {
	// CreateWorkspace tạo workspace mới, đo metrics và ủy quyền cho repo thực hiện insert
	CreateWorkspace(ctx context.Context, input coreEntity.CreateWorkspaceInput) (*coreEntity.Workspace, error)
}
