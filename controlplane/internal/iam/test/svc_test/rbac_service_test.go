package svc_test

import (
	"context"
	"errors"
	"testing"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcImpl "controlplane/internal/iam/service"

	"github.com/google/uuid"
)

type rbacRepoMock struct {
	getUserRolePermissionsFn   func(ctx context.Context, userID uuid.UUID, workspaceID uuid.UUID) ([]byte, error)
	getTenantRolePermissionsFn func(ctx context.Context, tenantID uuid.UUID, workspaceID uuid.UUID, roleID uuid.UUID) ([]byte, error)
	assignUserRoleFn           func(ctx context.Context, userRole *iamEntity.UserRole) error
	assignTenantRoleFn         func(ctx context.Context, tenantRole *iamEntity.TenantRole) error
	getRoleIDByUserIDFn        func(ctx context.Context, userID uuid.UUID) (string, int32, error)
	getRoleIDByTenantIDFn      func(ctx context.Context, tenantID uuid.UUID) (string, int32, error)
	listPlatformRolesFn        func(ctx context.Context) ([]iamEntity.Role, error)
	listTenantRolesFn          func(ctx context.Context, tenantID uuid.UUID) ([]iamEntity.Role, error)
}

var _ iamRepoInterface.RbacRepository = (*rbacRepoMock)(nil)

func (m *rbacRepoMock) GetUserRolePermissions(ctx context.Context, userID uuid.UUID, workspaceID uuid.UUID) ([]byte, error) {
	if m.getUserRolePermissionsFn != nil {
		return m.getUserRolePermissionsFn(ctx, userID, workspaceID)
	}
	return nil, nil
}

func (m *rbacRepoMock) GetTenantRolePermissions(ctx context.Context, tenantID uuid.UUID, workspaceID uuid.UUID, roleID uuid.UUID) ([]byte, error) {
	if m.getTenantRolePermissionsFn != nil {
		return m.getTenantRolePermissionsFn(ctx, tenantID, workspaceID, roleID)
	}
	return nil, nil
}

func (m *rbacRepoMock) AssignUserRole(ctx context.Context, userRole *iamEntity.UserRole) error {
	if m.assignUserRoleFn != nil {
		return m.assignUserRoleFn(ctx, userRole)
	}
	return nil
}

func (m *rbacRepoMock) AssignTenantRole(ctx context.Context, tenantRole *iamEntity.TenantRole) error {
	if m.assignTenantRoleFn != nil {
		return m.assignTenantRoleFn(ctx, tenantRole)
	}
	return nil
}

func (m *rbacRepoMock) GetRoleIDByUserID(ctx context.Context, userID uuid.UUID) (string, int32, error) {
	if m.getRoleIDByUserIDFn != nil {
		return m.getRoleIDByUserIDFn(ctx, userID)
	}
	return "", 0, nil
}

func (m *rbacRepoMock) GetRoleIDByTenantID(ctx context.Context, tenantID uuid.UUID) (string, int32, error) {
	if m.getRoleIDByTenantIDFn != nil {
		return m.getRoleIDByTenantIDFn(ctx, tenantID)
	}
	return "", 0, nil
}

func (m *rbacRepoMock) ListPlatformRoles(ctx context.Context) ([]iamEntity.Role, error) {
	if m.listPlatformRolesFn != nil {
		return m.listPlatformRolesFn(ctx)
	}
	return nil, nil
}

func (m *rbacRepoMock) ListTenantRoles(ctx context.Context, tenantID uuid.UUID) ([]iamEntity.Role, error) {
	if m.listTenantRolesFn != nil {
		return m.listTenantRolesFn(ctx, tenantID)
	}
	return nil, nil
}

// TestRbacService_ListPlatformRoles kiểm tra logic service chuyển tiếp truy vấn list platform roles.
func TestRbacService_ListPlatformRoles(t *testing.T) {
	mockRoles := []iamEntity.Role{
		{
			ID:        uuid.New(),
			Code:      "platform_admin",
			Name:      "Admin",
			RoleLevel: 1,
			Scope:     "platform",
		},
	}
	repo := &rbacRepoMock{
		listPlatformRolesFn: func(ctx context.Context) ([]iamEntity.Role, error) {
			return mockRoles, nil
		},
	}
	svc := iamSvcImpl.NewRbacService(repo, nil)

	roles, err := svc.ListPlatformRoles(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(roles) != 1 || roles[0].Code != "platform_admin" {
		t.Errorf("unexpected roles result returned: %+v", roles)
	}
}

// TestRbacService_ListPlatformRoles_Error kiểm tra logic service xử lý lỗi từ repo.
func TestRbacService_ListPlatformRoles_Error(t *testing.T) {
	expectedErr := errors.New("db query error")
	repo := &rbacRepoMock{
		listPlatformRolesFn: func(ctx context.Context) ([]iamEntity.Role, error) {
			return nil, expectedErr
		},
	}
	svc := iamSvcImpl.NewRbacService(repo, nil)

	_, err := svc.ListPlatformRoles(context.Background())
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
}

// TestRbacService_ListTenantRoles kiểm tra logic service chuyển tiếp truy vấn list tenant roles.
func TestRbacService_ListTenantRoles(t *testing.T) {
	tenantID := uuid.New()
	mockRoles := []iamEntity.Role{
		{
			ID:        uuid.New(),
			Code:      "tenant_admin",
			Name:      "Tenant Administrator",
			RoleLevel: 2,
			Scope:     "tenant",
		},
	}
	repo := &rbacRepoMock{
		listTenantRolesFn: func(ctx context.Context, tid uuid.UUID) ([]iamEntity.Role, error) {
			if tid != tenantID {
				return nil, errors.New("unexpected tenant id")
			}
			return mockRoles, nil
		},
	}
	svc := iamSvcImpl.NewRbacService(repo, nil)

	roles, err := svc.ListTenantRoles(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(roles) != 1 || roles[0].Code != "tenant_admin" {
		t.Errorf("unexpected roles result returned: %+v", roles)
	}
}

// TestRbacService_ListTenantRoles_Error kiểm tra logic service xử lý lỗi từ repo khi query tenant roles.
func TestRbacService_ListTenantRoles_Error(t *testing.T) {
	tenantID := uuid.New()
	expectedErr := errors.New("db error query tenant roles")
	repo := &rbacRepoMock{
		listTenantRolesFn: func(ctx context.Context, tid uuid.UUID) ([]iamEntity.Role, error) {
			return nil, expectedErr
		},
	}
	svc := iamSvcImpl.NewRbacService(repo, nil)

	_, err := svc.ListTenantRoles(context.Background(), tenantID)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
}
