package svc_test

import (
	"context"
	"errors"
	"testing"

	coreEntity "controlplane/internal/hierarchy/domain/entity"
	coreSvcImpl "controlplane/internal/hierarchy/service"
	coreErrorx "controlplane/internal/hierarchy/taxonomy"

	"github.com/google/uuid"
)

// [COMMENT]: fakeWorkspaceRepo giả lập repository cho workspace service tests
type fakeWorkspaceRepo struct {
	workspace *coreEntity.Workspace
	err       error
}

// [COMMENT]: CreateWorkspace triển khai fake repo cho test case
func (f *fakeWorkspaceRepo) CreateWorkspace(ctx context.Context, w coreEntity.Workspace) (*coreEntity.Workspace, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.workspace = &w
	return f.workspace, nil
}

// [COMMENT]: Test case kiểm thử tính chính xác của nghiệp vụ tạo Workspace
func TestWorkspaceService_CreateWorkspace(t *testing.T) {
	ctx := context.Background()

	t.Run("success create workspace", func(t *testing.T) {
		repo := &fakeWorkspaceRepo{}
		svc := coreSvcImpl.NewWorkspaceService(repo)

		zoneID, _ := uuid.NewV7()
		tenantID, _ := uuid.NewV7()
		ownerID, _ := uuid.NewV7()

		w, err := svc.CreateWorkspace(ctx, coreEntity.CreateWorkspaceInput{
			Name:     "Test Workspace",
			ZoneID:   zoneID,
			TenantID: &tenantID,
			OwnerID:  ownerID,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if w.Name != "Test Workspace" {
			t.Errorf("expected name Test Workspace, got %s", w.Name)
		}
		if w.ZoneID != zoneID {
			t.Errorf("expected zoneID %v, got %v", zoneID, w.ZoneID)
		}
		if *w.TenantID != tenantID {
			t.Errorf("expected tenantID %v, got %v", tenantID, w.TenantID)
		}
		if w.OwnerID != ownerID {
			t.Errorf("expected ownerID %v, got %v", ownerID, w.OwnerID)
		}
		if w.Status != coreEntity.WorkspaceStatusActive {
			t.Errorf("expected status active, got %s", w.Status)
		}
	})

	t.Run("error empty name", func(t *testing.T) {
		repo := &fakeWorkspaceRepo{}
		svc := coreSvcImpl.NewWorkspaceService(repo)

		_, err := svc.CreateWorkspace(ctx, coreEntity.CreateWorkspaceInput{
			Name:   "",
			ZoneID: uuid.New(),
		})

		if !errors.Is(err, coreErrorx.ErrWorkspaceInvalidInput) {
			t.Errorf("expected ErrWorkspaceInvalidInput, got %v", err)
		}
	})

	t.Run("error repo failed", func(t *testing.T) {
		repo := &fakeWorkspaceRepo{err: coreErrorx.ErrWorkspaceZoneNotFound}
		svc := coreSvcImpl.NewWorkspaceService(repo)

		_, err := svc.CreateWorkspace(ctx, coreEntity.CreateWorkspaceInput{
			Name:   "Fail WS",
			ZoneID: uuid.New(),
		})

		if !errors.Is(err, coreErrorx.ErrWorkspaceZoneNotFound) {
			t.Errorf("expected ErrWorkspaceZoneNotFound, got %v", err)
		}
	})
}
