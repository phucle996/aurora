package transport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamErrorx "controlplane/internal/iam/errorx"
	handler "controlplane/internal/iam/transport/http/handler"

	"github.com/gin-gonic/gin"
)

type rbacServiceStub struct {
	createRoleFn func(ctx context.Context, role *iamEntity.Role) error
}

var _ iamSvcInterface.RbacService = (*rbacServiceStub)(nil)

func (s *rbacServiceStub) Authorize(ctx context.Context, roleCode, permission string) (iamSvcInterface.AuthorizeResult, error) {
	return iamSvcInterface.AuthorizeAllow, nil
}
func (s *rbacServiceStub) LoadRole(ctx context.Context, role string) (iamSvcInterface.RoleEntry, error) {
	return iamSvcInterface.RoleEntry{}, nil
}
func (s *rbacServiceStub) InvalidateRole(ctx context.Context, role string) {}
func (s *rbacServiceStub) InvalidateAll(ctx context.Context)               {}
func (s *rbacServiceStub) WarmUp(ctx context.Context) error                { return nil }
func (s *rbacServiceStub) ListRoles(ctx context.Context) ([]*iamEntity.Role, error) {
	return nil, nil
}
func (s *rbacServiceStub) GetRole(ctx context.Context, id string) (*iamEntity.RoleWithPermissions, error) {
	return nil, nil
}
func (s *rbacServiceStub) CreateRole(ctx context.Context, role *iamEntity.Role) error {
	if s.createRoleFn != nil {
		return s.createRoleFn(ctx, role)
	}
	return nil
}
func (s *rbacServiceStub) UpdateRole(ctx context.Context, role *iamEntity.Role) error { return nil }
func (s *rbacServiceStub) DeleteRole(ctx context.Context, id string) error            { return nil }
func (s *rbacServiceStub) ListPermissions(ctx context.Context) ([]*iamEntity.Permission, error) {
	return nil, nil
}
func (s *rbacServiceStub) CreatePermission(ctx context.Context, perm *iamEntity.Permission) error {
	return nil
}
func (s *rbacServiceStub) AssignPermission(ctx context.Context, roleID, permID string) error {
	return nil
}
func (s *rbacServiceStub) RevokePermission(ctx context.Context, roleID, permID string) error {
	return nil
}
func (s *rbacServiceStub) AssignUserRole(ctx context.Context, userID, roleID string) error {
	return nil
}
func (s *rbacServiceStub) RevokeUserRole(ctx context.Context, userID, roleID string) error {
	return nil
}

func TestRbacHandlerCreateRoleInvalidArgument(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handler.NewRbacHandler(&rbacServiceStub{createRoleFn: func(ctx context.Context, role *iamEntity.Role) error {
		return iamErrorx.ErrInvalidArgument
	}})
	r.POST("/admin/rbac/roles", h.CreateRole)

	body, _ := json.Marshal(map[string]any{"code": "admin", "name": "Admin"})
	req := httptest.NewRequest(http.MethodPost, "/admin/rbac/roles", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 got %d", w.Code)
	}
}

func TestRbacHandlerCreateRoleNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handler.NewRbacHandler(&rbacServiceStub{createRoleFn: func(ctx context.Context, role *iamEntity.Role) error {
		return iamErrorx.ErrRoleNotFound
	}})
	r.POST("/admin/rbac/roles", h.CreateRole)

	body, _ := json.Marshal(map[string]any{"code": "admin", "name": "Admin"})
	req := httptest.NewRequest(http.MethodPost, "/admin/rbac/roles", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", w.Code)
	}
}

func TestRbacHandlerCreateRoleInternal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handler.NewRbacHandler(&rbacServiceStub{createRoleFn: func(ctx context.Context, role *iamEntity.Role) error {
		return errors.New("db timeout")
	}})
	r.POST("/admin/rbac/roles", h.CreateRole)

	body, _ := json.Marshal(map[string]any{"code": "admin", "name": "Admin"})
	req := httptest.NewRequest(http.MethodPost, "/admin/rbac/roles", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 got %d", w.Code)
	}
}
