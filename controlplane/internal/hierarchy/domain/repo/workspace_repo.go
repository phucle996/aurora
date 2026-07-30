package hierarchyRepoInterface

import (
	"context"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
)

type PersonalWorkspaceRepository interface {
	CreateWorkspaceForPersonal(context.Context, *hierarchyEntity.CreatePersonalWorkspace) (*hierarchyEntity.CreatePersonalWorkspace, error)
	ListWorkspacesForPersonal(context.Context, *hierarchyEntity.ListPersonalWorkspaces) ([]hierarchyEntity.ListPersonalWorkspaces, error)
	ListWorkspaceCatalogForPersonal(context.Context, *hierarchyEntity.ListPersonalWorkspaceCatalog) ([]hierarchyEntity.ListPersonalWorkspaceCatalog, error)
	DeleteWorkspaceForPersonal(context.Context, *hierarchyEntity.DeletePersonalWorkspace) error
}

type TenantWorkspaceRepository interface {
	CreateWorkspaceForTenant(context.Context, *hierarchyEntity.CreateTenantWorkspace) (*hierarchyEntity.CreateTenantWorkspace, error)
	ListWorkspacesForTenant(context.Context, *hierarchyEntity.ListTenantWorkspaces) ([]hierarchyEntity.ListTenantWorkspaces, error)
	ListWorkspaceCatalogForTenant(context.Context, *hierarchyEntity.ListTenantWorkspaceCatalog) ([]hierarchyEntity.ListTenantWorkspaceCatalog, error)
	DeleteWorkspaceForTenant(context.Context, *hierarchyEntity.DeleteTenantWorkspace) error
}
