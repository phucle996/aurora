package zoneSvcInterface

import (
	"context"
	coreEntity "controlplane/internal/hierarchy/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: TenantWorkspaceService quản lý các nghiệp vụ Workspace dành riêng cho đối tượng Doanh nghiệp (Enterprise).
type TenantWorkspaceService interface {
	// CreateWorkspaceForTenant tạo workspace mới cho Tenant, kiểm tra ràng buộc zone và tenant active
	CreateWorkspaceForTenant(ctx context.Context, workspace coreEntity.TenantWorkspace) (*coreEntity.TenantWorkspace, error)
 
	// GetWorkspaceForTenant xem chi tiết thông tin workspace thuộc Tenant
	GetWorkspaceForTenant(ctx context.Context, workspaceID uuid.UUID) (*coreEntity.TenantWorkspace, error)
 
	// ListWorkspacesForTenant lấy danh sách các workspace thuộc Tenant mà user có quyền read
	ListWorkspacesForTenant(ctx context.Context, tenantID uuid.UUID, userID uuid.UUID, roleID uuid.UUID) ([]*coreEntity.TenantWorkspace, error)
 
	// ListWorkspaceCatalogForTenant lấy catalog workspace thuộc Tenant trong Zone cụ thể
	ListWorkspaceCatalogForTenant(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID, userID uuid.UUID, roleID uuid.UUID) ([]coreEntity.WorkspaceCatalog, error)

	// UpdateWorkspaceForTenant cập nhật cấu hình/thông tin workspace thuộc Tenant
	UpdateWorkspaceForTenant(ctx context.Context, workspace coreEntity.TenantWorkspace) (*coreEntity.TenantWorkspace, error)
 
	// DeleteWorkspaceForTenant xóa workspace thuộc Tenant
	DeleteWorkspaceForTenant(ctx context.Context, workspaceID uuid.UUID) error
}
 
// [COMMENT]: PersonalWorkspaceService quản lý các nghiệp vụ Workspace dành riêng cho cá nhân (Personal Owner).
type PersonalWorkspaceService interface {
	// CreateWorkspaceForPersonal tạo workspace mới cho tài khoản cá nhân, kiểm tra zone active
	CreateWorkspaceForPersonal(ctx context.Context, workspace coreEntity.PersonalWorkspace) (*coreEntity.PersonalWorkspace, error)
 
	// GetWorkspaceForPersonal xem chi tiết thông tin workspace cá nhân
	GetWorkspaceForPersonal(ctx context.Context, workspaceID uuid.UUID) (*coreEntity.PersonalWorkspace, error)
 
	// ListWorkspacesForPersonal lấy danh sách các workspace cá nhân do user sở hữu hoặc được share quyền
	ListWorkspacesForPersonal(ctx context.Context, userID uuid.UUID) ([]*coreEntity.WorkspacePersonalListItem, error)
 
	// ListWorkspaceCatalogForPersonal lấy catalog workspace cá nhân do user sở hữu trong Zone cụ thể
	ListWorkspaceCatalogForPersonal(ctx context.Context, userID uuid.UUID, zoneID uuid.UUID) ([]coreEntity.WorkspaceCatalog, error)

	// UpdateWorkspaceForPersonal cập nhật cấu hình/thông tin workspace cá nhân
	UpdateWorkspaceForPersonal(ctx context.Context, workspace coreEntity.PersonalWorkspace) (*coreEntity.PersonalWorkspace, error)
 
	// DeleteWorkspaceForPersonal xóa workspace cá nhân
	DeleteWorkspaceForPersonal(ctx context.Context, workspaceID uuid.UUID) error
}
