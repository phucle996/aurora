package svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	coreEntity "controlplane/internal/hierarchy/domain/entity"
	coreSvcImpl "controlplane/internal/hierarchy/service"
	coreErrorx "controlplane/internal/hierarchy/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"

	"github.com/google/uuid"
)

// [COMMENT]: fakeTenantWorkspaceRepo giả lập repository cho tenant workspace service tests
type fakeTenantWorkspaceRepo struct {
	workspace *coreEntity.TenantWorkspace
	err       error
}

func (f *fakeTenantWorkspaceRepo) Create(ctx context.Context, w coreEntity.TenantWorkspace) (*coreEntity.TenantWorkspace, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.workspace = &w
	return f.workspace, nil
}

func (f *fakeTenantWorkspaceRepo) GetByID(ctx context.Context, id uuid.UUID) (*coreEntity.TenantWorkspace, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.workspace, nil
}

func (f *fakeTenantWorkspaceRepo) Update(ctx context.Context, w coreEntity.TenantWorkspace) (*coreEntity.TenantWorkspace, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.workspace = &w
	return f.workspace, nil
}

func (f *fakeTenantWorkspaceRepo) Delete(ctx context.Context, id uuid.UUID, tenantID uuid.UUID) error {
	return f.err
}

func (f *fakeTenantWorkspaceRepo) ListAllByTenant(ctx context.Context, tenantID uuid.UUID) ([]*coreEntity.TenantWorkspace, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []*coreEntity.TenantWorkspace{f.workspace}, nil
}

func (f *fakeTenantWorkspaceRepo) ListByTenantAndIDs(ctx context.Context, tenantID uuid.UUID, ids []uuid.UUID) ([]*coreEntity.TenantWorkspace, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []*coreEntity.TenantWorkspace{f.workspace}, nil
}

// [COMMENT]: ListCatalogAllByTenant stub — trả về catalog tối giản cho test
func (f *fakeTenantWorkspaceRepo) ListCatalogAllByTenant(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID) ([]coreEntity.WorkspaceCatalog, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.workspace == nil {
		return []coreEntity.WorkspaceCatalog{}, nil
	}
	return []coreEntity.WorkspaceCatalog{{ID: f.workspace.ID, Code: f.workspace.Code, Name: f.workspace.Name}}, nil
}

// [COMMENT]: ListCatalogByTenantAndIDs stub — trả về catalog theo IDs cụ thể cho test
func (f *fakeTenantWorkspaceRepo) ListCatalogByTenantAndIDs(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID, ids []uuid.UUID) ([]coreEntity.WorkspaceCatalog, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.workspace == nil {
		return []coreEntity.WorkspaceCatalog{}, nil
	}
	return []coreEntity.WorkspaceCatalog{{ID: f.workspace.ID, Code: f.workspace.Code, Name: f.workspace.Name}}, nil
}

// [COMMENT]: fakePersonalWorkspaceRepo giả lập repository cho personal workspace service tests
type fakePersonalWorkspaceRepo struct {
	workspace *coreEntity.PersonalWorkspace
	err       error
}

func (f *fakePersonalWorkspaceRepo) Create(ctx context.Context, w coreEntity.PersonalWorkspace) (*coreEntity.PersonalWorkspace, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.workspace = &w
	return f.workspace, nil
}

func (f *fakePersonalWorkspaceRepo) GetByID(ctx context.Context, id uuid.UUID) (*coreEntity.PersonalWorkspace, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.workspace, nil
}

func (f *fakePersonalWorkspaceRepo) ListByOwner(ctx context.Context, ownerID uuid.UUID) ([]*coreEntity.WorkspacePersonalListItem, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.workspace == nil {
		return []*coreEntity.WorkspacePersonalListItem{}, nil
	}
	return []*coreEntity.WorkspacePersonalListItem{
		{
			ID:          f.workspace.ID,
			Name:        f.workspace.Name,
			Code:        f.workspace.Code,
			Description: f.workspace.Description,
			CreatedAt:   f.workspace.CreatedAt,
		},
	}, nil
}

// [COMMENT]: ListCatalogByOwner stub — trả về catalog tối giản theo owner cho test
func (f *fakePersonalWorkspaceRepo) ListCatalogByOwner(ctx context.Context, ownerID uuid.UUID, zoneID uuid.UUID) ([]coreEntity.WorkspaceCatalog, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.workspace == nil {
		return []coreEntity.WorkspaceCatalog{}, nil
	}
	return []coreEntity.WorkspaceCatalog{{ID: f.workspace.ID, Code: f.workspace.Code, Name: f.workspace.Name}}, nil
}

func (f *fakePersonalWorkspaceRepo) Update(ctx context.Context, w coreEntity.PersonalWorkspace) (*coreEntity.PersonalWorkspace, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.workspace = &w
	return f.workspace, nil
}

func (f *fakePersonalWorkspaceRepo) Delete(ctx context.Context, id uuid.UUID, ownerID uuid.UUID) error {
	return f.err
}

// [COMMENT]: Test case kiểm thử tính chính xác của Tenant và Personal Workspace Services
func TestWorkspaceService(t *testing.T) {
	ctx := context.Background()

	// [COMMENT]: Khởi tạo L1 cache registry cho Service
	l1 := cacheengine.NewL1Cache()
	registry := cacheengine.NewCacheRegistry(l1)

	t.Run("success create tenant workspace", func(t *testing.T) {
		repo := &fakeTenantWorkspaceRepo{}
		svc := coreSvcImpl.NewTenantWorkspaceService(repo, registry)

		zoneID, _ := uuid.NewV7()
		tenantID, _ := uuid.NewV7()
		ownerID, _ := uuid.NewV7()

		w, err := svc.CreateWorkspaceForTenant(ctx, coreEntity.TenantWorkspace{
			Name:     "Tenant Workspace",
			Code:     "tenant-ws",
			ZoneID:   zoneID,
			TenantID: tenantID,
			OwnerID:  ownerID,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if w.Name != "Tenant Workspace" {
			t.Errorf("expected name Tenant Workspace, got %s", w.Name)
		}
		if w.Code != "tenant-ws" {
			t.Errorf("expected code tenant-ws, got %s", w.Code)
		}
	})

	t.Run("success list workspaces for tenant - wildcard admin", func(t *testing.T) {
		tenantID, _ := uuid.NewV7()
		roleID, _ := uuid.NewV7()
		userID, _ := uuid.NewV7()

		// [COMMENT]: Mock data cache tenant_role trả về wildcard permission (*)
		cacheengine.Register(registry, "tenant_role", 15*time.Minute, func(ctx context.Context, param string) (*iamproto.RoleEntry, error) {
			return &iamproto.RoleEntry{
				Permissions: []string{
					tenantID.String() + ":00000000-0000-0000-0000-000000000000:hierarchy:workspace:read",
				},
			}, nil
		})

		repo := &fakeTenantWorkspaceRepo{
			workspace: &coreEntity.TenantWorkspace{
				Name:     "Wildcard Workspace",
				TenantID: tenantID,
			},
		}
		svc := coreSvcImpl.NewTenantWorkspaceService(repo, registry)

		list, err := svc.ListWorkspacesForTenant(ctx, tenantID, userID, roleID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(list) != 1 || list[0].Name != "Wildcard Workspace" {
			t.Errorf("expected Wildcard Workspace, got %v", list)
		}
	})

	t.Run("success create personal workspace", func(t *testing.T) {
		repo := &fakePersonalWorkspaceRepo{}
		svc := coreSvcImpl.NewPersonalWorkspaceService(repo)

		zoneID, _ := uuid.NewV7()
		ownerID, _ := uuid.NewV7()

		w, err := svc.CreateWorkspaceForPersonal(ctx, coreEntity.PersonalWorkspace{
			Name:    "Personal Workspace",
			Code:    "personal-ws",
			ZoneID:  zoneID,
			OwnerID: ownerID,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if w.Name != "Personal Workspace" {
			t.Errorf("expected name Personal Workspace, got %s", w.Name)
		}
	})

	t.Run("error repo failed for personal", func(t *testing.T) {
		repo := &fakePersonalWorkspaceRepo{err: coreErrorx.ErrZoneNotFound}
		svc := coreSvcImpl.NewPersonalWorkspaceService(repo)

		_, err := svc.CreateWorkspaceForPersonal(ctx, coreEntity.PersonalWorkspace{
			Name:   "Fail WS",
			Code:   "fail-ws",
			ZoneID: uuid.New(),
		})

		if !errors.Is(err, coreErrorx.ErrZoneNotFound) {
			t.Errorf("expected ErrZoneNotFound, got %v", err)
		}
	})
}
