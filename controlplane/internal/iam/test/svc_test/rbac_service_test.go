package svc_test

import (
	"context"
	"errors"
	"testing"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcImpl "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type rbacRepoMock struct {
	getRoleByIDFn   func(ctx context.Context, id string) (*iamEntity.Role, error)
	getRoleByCodeFn func(ctx context.Context, code string) (*iamEntity.RoleWithPermissions, error)
	listRolesFn     func(ctx context.Context) ([]*iamEntity.Role, error)
}

var _ iamRepoInterface.RbacRepository = (*rbacRepoMock)(nil)

func (m *rbacRepoMock) GetRoleByCode(ctx context.Context, code string) (*iamEntity.RoleWithPermissions, error) {
	if m.getRoleByCodeFn != nil {
		return m.getRoleByCodeFn(ctx, code)
	}
	return nil, nil
}
func (m *rbacRepoMock) ListRoleEntries(ctx context.Context) ([]*iamEntity.RoleWithPermissions, error) {
	return nil, nil
}
func (m *rbacRepoMock) ListRoles(ctx context.Context) ([]*iamEntity.Role, error) {
	if m.listRolesFn != nil {
		return m.listRolesFn(ctx)
	}
	return nil, nil
}
func (m *rbacRepoMock) GetRoleByID(ctx context.Context, id string) (*iamEntity.Role, error) {
	if m.getRoleByIDFn != nil {
		return m.getRoleByIDFn(ctx, id)
	}
	return nil, nil
}
func (m *rbacRepoMock) CreateRole(ctx context.Context, role *iamEntity.Role) error { return nil }
func (m *rbacRepoMock) UpdateRole(ctx context.Context, role *iamEntity.Role) error { return nil }
func (m *rbacRepoMock) DeleteRole(ctx context.Context, id uuid.UUID) error            { return nil }
func (m *rbacRepoMock) ListPermissions(ctx context.Context) ([]*iamEntity.Permission, error) {
	return nil, nil
}
func (m *rbacRepoMock) GetPermissionByID(ctx context.Context, id string) (*iamEntity.Permission, error) {
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
func (m *rbacRepoMock) AssignUserRole(ctx context.Context, userID, roleID string) error { return nil }
func (m *rbacRepoMock) RevokeUserRole(ctx context.Context, userID, roleID string) error { return nil }

func TestRbacServiceGetRoleInvalidUUIDMapsInvalidArgument(t *testing.T) {
	svc := iamSvcImpl.NewRbacService(&rbacRepoMock{getRoleByIDFn: func(ctx context.Context, id string) (*iamEntity.Role, error) {
		_, err := uuid.Parse("not-a-uuid")
		return nil, err
	}}, nil, nil)

	_, err := svc.GetRole(context.Background(), "bad-id")
	if !errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
	_, ok := apperr.As(err)
	if ok {
		t.Fatalf("expected raw error, not wrapped app error envelope")
	}
}

func TestRbacServiceGetRoleNoRowsMapsRoleNotFound(t *testing.T) {
	svc := iamSvcImpl.NewRbacService(&rbacRepoMock{getRoleByIDFn: func(ctx context.Context, id string) (*iamEntity.Role, error) {
		return nil, pgx.ErrNoRows
	}}, nil, nil)

	_, err := svc.GetRole(context.Background(), uuid.NewString())
	if !errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
		t.Fatalf("expected ErrRoleNotFound, got %v", err)
	}
	_, ok := apperr.As(err)
	if ok {
		t.Fatalf("expected raw error, not wrapped app error envelope")
	}
}

func TestRbacServiceListRolesDependencyMapsInternal(t *testing.T) {
	raw := errors.New("db down")
	svc := iamSvcImpl.NewRbacService(&rbacRepoMock{listRolesFn: func(ctx context.Context) ([]*iamEntity.Role, error) {
		return nil, raw
	}}, nil, nil)

	_, err := svc.ListRoles(context.Background())
	if !errors.Is(err, iamTaxonomy.ErrAuthenticationUnavailable) {
		t.Fatalf("expected ErrAuthenticationUnavailable, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Outcome != iamTaxonomy.RbacOutcomeDependencyError {
		t.Fatalf("unexpected outcome: %q", appErr.Outcome)
	}
	if !errors.Is(appErr.Cause, raw) {
		t.Fatalf("expected raw cause preserved")
	}
}
