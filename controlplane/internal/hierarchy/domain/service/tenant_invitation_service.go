package hierarchySvcInterface

import (
	"context"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
)

type TenantInvitationService interface {
	CreateTenantInvitation(context.Context, *hierarchyEntity.CreateTenantInvitation) (*hierarchyEntity.CreateTenantInvitation, error)
	PreviewTenantInvitation(context.Context, *hierarchyEntity.PreviewTenantInvitation) (*hierarchyEntity.PreviewTenantInvitation, error)
	RevokeTenantInvitation(context.Context, *hierarchyEntity.RevokeTenantInvitation) (*hierarchyEntity.RevokeTenantInvitation, error)
	JoinTenantInvitation(context.Context, *hierarchyEntity.JoinTenantInvitation) (*hierarchyEntity.JoinTenantInvitation, error)
}
