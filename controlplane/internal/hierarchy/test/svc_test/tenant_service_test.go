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

// [COMMENT]: fakeTenantRepo giả lập repository cho tenant service tests
type fakeTenantRepo struct {
	tenant *coreEntity.Tenant
	err    error
}

// [COMMENT]: CreateTenant triển khai fake repo cho test case
func (f *fakeTenantRepo) CreateTenant(ctx context.Context, t coreEntity.Tenant, ownerID uuid.UUID) (*coreEntity.Tenant, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.tenant = &t
	return f.tenant, nil
}

func (f *fakeTenantRepo) ResolveTenantByDomain(ctx context.Context, domain string) (*coreEntity.Tenant, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.tenant != nil && f.tenant.Domain == domain {
		return f.tenant, nil
	}
	return nil, coreErrorx.ErrTenantNotFound
}

func (f *fakeTenantRepo) ListTenantsPaged(ctx context.Context, limit, offset int) ([]coreEntity.Tenant, bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	if f.tenant == nil {
		return []coreEntity.Tenant{}, false, nil
	}
	return []coreEntity.Tenant{*f.tenant}, false, nil
}

func (f *fakeTenantRepo) CheckMembership(ctx context.Context, tenantID, userID uuid.UUID) (bool, string, error) {
	if f.err != nil {
		return false, "", f.err
	}
	return true, "member", nil
}

// [COMMENT]: Test case kiểm thử tính chính xác của nghiệp vụ tạo Tenant
func TestTenantService_CreateTenant(t *testing.T) {
	ctx := context.Background()

	t.Run("success create tenant", func(t *testing.T) {
		repo := &fakeTenantRepo{}
		svc := coreSvcImpl.NewTenantService(repo)

		ownerID, _ := uuid.NewV7()

		ten, err := svc.CreateTenant(ctx, coreEntity.Tenant{
			Name: "Acme Corp",
			Code: "acme",
		}, ownerID)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if ten.Name != "Acme Corp" {
			t.Errorf("expected name Acme Corp, got %s", ten.Name)
		}
		if ten.Code != "acme" {
			t.Errorf("expected code acme, got %s", ten.Code)
		}
		if ten.Status != coreEntity.TenantStatusActive {
			t.Errorf("expected status active, got %s", ten.Status)
		}
	})

	t.Run("error repo conflict", func(t *testing.T) {
		repo := &fakeTenantRepo{err: coreErrorx.ErrCodeAlreadyExists}
		svc := coreSvcImpl.NewTenantService(repo)

		_, err := svc.CreateTenant(ctx, coreEntity.Tenant{
			Name: "Acme Corp",
			Code: "acme",
		}, uuid.New())

		if !errors.Is(err, coreErrorx.ErrCodeAlreadyExists) {
			t.Errorf("expected ErrTenantCodeAlreadyExists, got %v", err)
		}
	})
}
