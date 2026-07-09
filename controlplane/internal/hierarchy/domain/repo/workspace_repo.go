package coreRepoInterface

import (
	"context"
	coreEntity "controlplane/internal/hierarchy/domain/entity"
	"github.com/google/uuid"
)

// [COMMENT]: TenantWorkspaceRepository định nghĩa các phương thức giao tiếp CSDL cho Workspace Doanh nghiệp (Enterprise).
type TenantWorkspaceRepository interface {
	// Create tạo workspace mới liên kết với Tenant, kiểm tra ràng buộc zone và tenant active
	Create(ctx context.Context, workspace coreEntity.TenantWorkspace) (*coreEntity.TenantWorkspace, error)
	
	// GetByID tìm kiếm thông tin chi tiết của một Workspace theo ID
	GetByID(ctx context.Context, id uuid.UUID) (*coreEntity.TenantWorkspace, error)
	
	// ListAllByTenant lấy toàn bộ workspace thuộc Tenant (cho Tenant Admin/Owner)
	ListAllByTenant(ctx context.Context, tenantID uuid.UUID) ([]*coreEntity.TenantWorkspace, error)
	
	// ListByTenantAndIDs lấy các workspace thuộc Tenant cụ thể theo danh sách IDs được duyệt
	ListByTenantAndIDs(ctx context.Context, tenantID uuid.UUID, ids []uuid.UUID) ([]*coreEntity.TenantWorkspace, error)
 
	// ListCatalogAllByTenant lấy catalog toàn bộ workspace thuộc Tenant trong Zone cụ thể
	ListCatalogAllByTenant(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID) ([]coreEntity.WorkspaceCatalog, error)
	
	// ListCatalogByTenantAndIDs lấy catalog các workspace thuộc Tenant cụ thể theo danh sách IDs được duyệt trong Zone
	ListCatalogByTenantAndIDs(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID, ids []uuid.UUID) ([]coreEntity.WorkspaceCatalog, error)
	
	// Update cập nhật thông tin của workspace
	Update(ctx context.Context, workspace coreEntity.TenantWorkspace) (*coreEntity.TenantWorkspace, error)
	
	// Delete xóa workspace thuộc Tenant ra khỏi database
	Delete(ctx context.Context, id uuid.UUID, tenantID uuid.UUID) error
}
 
// [COMMENT]: PersonalWorkspaceRepository định nghĩa các phương thức giao tiếp CSDL cho Workspace Cá nhân (Individual).
type PersonalWorkspaceRepository interface {
	// Create tạo workspace cá nhân mới, kiểm tra ràng buộc zone active
	Create(ctx context.Context, workspace coreEntity.PersonalWorkspace) (*coreEntity.PersonalWorkspace, error)
	
	// GetByID tìm kiếm thông tin chi tiết của một Workspace cá nhân theo ID
	GetByID(ctx context.Context, id uuid.UUID) (*coreEntity.PersonalWorkspace, error)
	
	// ListByOwner lấy toàn bộ workspace cá nhân do user sở hữu (tránh orphan workspace)
	ListByOwner(ctx context.Context, ownerID uuid.UUID) ([]*coreEntity.WorkspacePersonalListItem, error)
 
	// ListCatalogByOwner lấy catalog toàn bộ workspace cá nhân do user sở hữu trong Zone cụ thể
	ListCatalogByOwner(ctx context.Context, ownerID uuid.UUID, zoneID uuid.UUID) ([]coreEntity.WorkspaceCatalog, error)
	
	// Update cập nhật thông tin của workspace cá nhân
	Update(ctx context.Context, workspace coreEntity.PersonalWorkspace) (*coreEntity.PersonalWorkspace, error)
	
	// Delete xóa workspace cá nhân ra khỏi database
	Delete(ctx context.Context, id uuid.UUID, ownerID uuid.UUID) error
}
