package hierarchyRepoInterface

import (
	"context"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
)

type TenantInvitationRepository interface {
	CreateTenantInvitation(context.Context, *hierarchyEntity.CreateTenantInvitation) (*hierarchyEntity.CreateTenantInvitation, error)
	PreviewTenantInvitation(context.Context, *hierarchyEntity.PreviewTenantInvitation) (*hierarchyEntity.PreviewTenantInvitation, error)
	RevokeTenantInvitation(context.Context, *hierarchyEntity.RevokeTenantInvitation) (*hierarchyEntity.RevokeTenantInvitation, error)
	JoinTenantInvitation(context.Context, *hierarchyEntity.JoinTenantInvitation) (*hierarchyEntity.JoinTenantInvitation, error)
}
