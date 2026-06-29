package svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcImpl "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/pkg/apperr"
	"controlplane/pkg/constant"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type rbacRepoMock struct {
	getRoleByIDFn                  func(ctx context.Context, id uuid.UUID) (*iamEntity.Role, error)
	getRoleByCodeFn                func(ctx context.Context, code string) (*iamEntity.RoleWithPermissions, error)
	listRolesFn                    func(ctx context.Context) ([]*iamEntity.Role, error)
	getPermissionCodesByRoleCodeFn func(ctx context.Context, roleCode string) ([]string, error)
	listSystemRoleEntriesFn        func(ctx context.Context) ([]*iamEntity.RoleWithPermissions, error)
}

var _ iamRepoInterface.RbacRepository = (*rbacRepoMock)(nil)

func (m *rbacRepoMock) GetRoleByCode(ctx context.Context, code string) (*iamEntity.RoleWithPermissions, error) {
	if m.getRoleByCodeFn != nil {
		return m.getRoleByCodeFn(ctx, code)
	}
	return nil, nil
}
func (m *rbacRepoMock) GetPermissionCodesByRoleCode(ctx context.Context, roleCode string) ([]string, error) {
	if m.getPermissionCodesByRoleCodeFn != nil {
		return m.getPermissionCodesByRoleCodeFn(ctx, roleCode)
	}
	return nil, nil
}
func (m *rbacRepoMock) ListSystemRoleEntries(ctx context.Context) ([]*iamEntity.RoleWithPermissions, error) {
	if m.listSystemRoleEntriesFn != nil {
		return m.listSystemRoleEntriesFn(ctx)
	}
	return nil, nil
}
func (m *rbacRepoMock) ListRoles(ctx context.Context) ([]*iamEntity.Role, error) {
	if m.listRolesFn != nil {
		return m.listRolesFn(ctx)
	}
	return nil, nil
}
func (m *rbacRepoMock) GetRoleByID(ctx context.Context, id uuid.UUID) (*iamEntity.Role, error) {
	if m.getRoleByIDFn != nil {
		return m.getRoleByIDFn(ctx, id)
	}
	return nil, nil
}
func (m *rbacRepoMock) CreateRole(ctx context.Context, role *iamEntity.Role) error { return nil }
func (m *rbacRepoMock) UpdateRole(ctx context.Context, role *iamEntity.Role) error { return nil }
func (m *rbacRepoMock) DeleteRole(ctx context.Context, id uuid.UUID) error         { return nil }
func (m *rbacRepoMock) ListPermissions(ctx context.Context) ([]*iamEntity.Permission, error) {
	return nil, nil
}
func (m *rbacRepoMock) GetPermissionByID(ctx context.Context, id uuid.UUID) (*iamEntity.Permission, error) {
	return nil, nil
}
func (m *rbacRepoMock) GetPermissionByCode(ctx context.Context, code string) (*iamEntity.Permission, error) {
	return nil, nil
}
func (m *rbacRepoMock) CreatePermission(ctx context.Context, perm *iamEntity.Permission) error {
	return nil
}
func (m *rbacRepoMock) AssignPermission(ctx context.Context, roleID, permissionID uuid.UUID) error {
	return nil
}
func (m *rbacRepoMock) RevokePermission(ctx context.Context, roleID, permissionID uuid.UUID) error {
	return nil
}
func (m *rbacRepoMock) AssignUserRole(ctx context.Context, userID, roleID uuid.UUID, scopeType iamEntity.RoleScopeType, tenantID, workspaceID *uuid.UUID, expiresAt *time.Time) error {
	return nil
}
func (m *rbacRepoMock) RevokeUserRole(ctx context.Context, userID, roleID uuid.UUID) error {
	return nil
}
func (m *rbacRepoMock) GetUserMaxRoleLevel(ctx context.Context, userID uuid.UUID) (int, error) {
	return 999999, nil
}
func (m *rbacRepoMock) GetUserRoleAndLevelByScope(ctx context.Context, userID uuid.UUID, scope string) (string, int, error) {
	return "platform_user", 8, nil
}
func (m *rbacRepoMock) GetUserPermissionsMerged(ctx context.Context, userID uuid.UUID) ([]string, error) {
	return nil, nil
}
func (m *rbacRepoMock) GetTenantCodeByID(ctx context.Context, tenantID uuid.UUID) (string, error) {
	return "", nil
}

func TestRbacServiceGetRoleNoRowsMapsRoleNotFound(t *testing.T) {
	roleID := uuid.New()
	svc := iamSvcImpl.NewRbacService(&rbacRepoMock{
		getRoleByIDFn: func(ctx context.Context, id uuid.UUID) (*iamEntity.Role, error) {
			return nil, pgx.ErrNoRows
		},
	}, nil)

	ident := &constant.Identity{Level: 0}
	ctx := context.WithValue(context.Background(), constant.IdentityKey, ident)
	_, err := svc.GetRole(ctx, roleID)
	if !errors.Is(err, iamTaxonomy.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	_, ok := apperr.As(err)
	if ok {
		t.Fatalf("expected raw error, not wrapped app error envelope")
	}
}

func TestRbacServiceListRolesDependencyMapsInternal(t *testing.T) {
	raw := errors.New("db down")
	svc := iamSvcImpl.NewRbacService(&rbacRepoMock{
		listRolesFn: func(ctx context.Context) ([]*iamEntity.Role, error) {
			return nil, raw
		},
	}, nil)

	ident := &constant.Identity{Level: 0}
	ctx := context.WithValue(context.Background(), constant.IdentityKey, ident)
	_, err := svc.ListRoles(ctx)
	if !errors.Is(err, iamTaxonomy.ErrAuthenticationUnavailable) {
		t.Fatalf("expected ErrAuthenticationUnavailable, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Outcome != "failure_unknown" {
		t.Fatalf("unexpected outcome: %q", appErr.Outcome)
	}
	if !errors.Is(appErr.Cause, raw) {
		t.Fatalf("expected raw cause preserved")
	}
}
