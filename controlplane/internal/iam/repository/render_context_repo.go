package iamRepoImpl

import (
	"context"
	"fmt"
	"strings"

	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
)

type RenderContextRepository struct {
	cacheEngine *cacheengine.CacheRegistry
}

func NewRenderContextRepository(cacheEngine *cacheengine.CacheRegistry) iamRepoInterface.RenderContextRepository {
	return &RenderContextRepository{cacheEngine: cacheEngine}
}

func (r *RenderContextRepository) GetPersonalRenderContext(ctx context.Context, workflow *iamEntity.PersonalRenderContext) error {
	value, err := r.cacheEngine.GetOrLoad(ctx, "user_role", workflow.UserID.String())
	if err != nil {
		return fmt.Errorf("render context repo: load personal assignment: %w", err)
	}
	entry, ok := value.(*iamproto.RoleEntry)
	if !ok || entry == nil {
		return fmt.Errorf("render context repo: personal cache value has unexpected type")
	}
	if len(entry.Permissions) == 0 {
		return iamTaxonomy.ErrActionNotAllowed
	}

	workflow.Permissions = make([]string, len(entry.Permissions))
	for index, permission := range entry.Permissions {
		// Compiled authorization is a security boundary. Corrupt durable/cache
		// data fails closed instead of producing a partially privileged UI.
		parts := strings.Split(permission, ":")
		if len(parts) != 5 || parts[0] == "" || parts[1] == "" || parts[2] == "" || parts[3] == "" || parts[4] == "" {
			return fmt.Errorf("render context repo: invalid compiled personal permission")
		}
		workflow.Permissions[index] = permission
	}
	return nil
}

func (r *RenderContextRepository) GetTenantRenderContext(ctx context.Context, workflow *iamEntity.TenantRenderContext) error {
	value, err := r.cacheEngine.GetOrLoad(
		ctx,
		"membership_role",
		workflow.UserID.String()+":"+workflow.TenantID.String(),
	)
	if err != nil {
		return fmt.Errorf("render context repo: load tenant assignment: %w", err)
	}
	entry, ok := value.(*iamproto.RoleEntry)
	if !ok || entry == nil {
		return fmt.Errorf("render context repo: tenant cache value has unexpected type")
	}
	if len(entry.Permissions) == 0 {
		return iamTaxonomy.ErrActionNotAllowed
	}

	tenantID := workflow.TenantID.String()
	workflow.Permissions = make([]string, len(entry.Permissions))
	for index, permission := range entry.Permissions {
		// The identity prefix must match the verified tenant exactly. A stale or
		// poisoned L1 entry must never leak another tenant's navigation.
		parts := strings.Split(permission, ":")
		if len(parts) != 5 || parts[0] != tenantID || parts[1] == "" || parts[2] == "" || parts[3] == "" || parts[4] == "" {
			return fmt.Errorf("render context repo: invalid compiled tenant permission")
		}
		workflow.Permissions[index] = permission
	}
	return nil
}
