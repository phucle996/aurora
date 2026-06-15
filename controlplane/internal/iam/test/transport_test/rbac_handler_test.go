package transport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	handler "controlplane/internal/iam/transport/http/handler"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type rbacServiceStub struct {
	createRoleFn func(ctx context.Context, role *iamEntity.Role) error
}

var _ iamSvcInterface.RbacService = (*rbacServiceStub)(nil)

func (s *rbacServiceStub) WarmUp(ctx context.Context) error { return nil }
func (s *rbacServiceStub) ListRoles(ctx context.Context) ([]*iamEntity.Role, error) {
	return nil, nil
}
func (s *rbacServiceStub) GetRole(ctx context.Context, id uuid.UUID) (*iamEntity.RoleWithPermissions, error) {
	return nil, nil
}
func (s *rbacServiceStub) CreateRole(ctx context.Context, role *iamEntity.Role) error {
	if s.createRoleFn != nil {
		return s.createRoleFn(ctx, role)
	}
	return nil
}
func (s *rbacServiceStub) UpdateRole(ctx context.Context, role *iamEntity.Role) error {
	return nil
}
func (s *rbacServiceStub) DeleteRole(ctx context.Context, id uuid.UUID) error {
	return nil
}
func (s *rbacServiceStub) ListPermissions(ctx context.Context) ([]*iamEntity.Permission, error) {
	return nil, nil
}
func (s *rbacServiceStub) AssignPermission(ctx context.Context, roleID, permID uuid.UUID) error {
	return nil
}
func (s *rbacServiceStub) RevokePermission(ctx context.Context, roleID, permID uuid.UUID) error {
	return nil
}
func (s *rbacServiceStub) AssignUserRole(ctx context.Context, userID, roleID uuid.UUID, scopeType iamEntity.RoleScopeType, tenantID, workspaceID *uuid.UUID, expiresAt *time.Time) error {
	return nil
}
func (s *rbacServiceStub) RevokeUserRole(ctx context.Context, userID, roleID uuid.UUID) error {
	return nil
}

func TestRbacHandlerCreateRoleInvalidArgument(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handler.NewRbacHandler(&rbacServiceStub{createRoleFn: func(ctx context.Context, role *iamEntity.Role) error {
		return iamTaxonomy.ErrInvalidArgument
	}})
	r.POST("/admin/rbac/roles", func(c *gin.Context) {
		ident := &constant.Identity{UserID: uuid.NewString()}
		ctx := context.WithValue(c.Request.Context(), constant.IdentityKey, ident)
		c.Request = c.Request.WithContext(ctx)
		h.CreateRole(c)
	})

	body, _ := json.Marshal(map[string]any{"code": "admin", "name": "Admin", "scope_type": "platform"})
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
		return iamTaxonomy.ErrRoleNotFound
	}})
	r.POST("/admin/rbac/roles", func(c *gin.Context) {
		ident := &constant.Identity{UserID: uuid.NewString()}
		ctx := context.WithValue(c.Request.Context(), constant.IdentityKey, ident)
		c.Request = c.Request.WithContext(ctx)
		h.CreateRole(c)
	})

	body, _ := json.Marshal(map[string]any{"code": "admin", "name": "Admin", "scope_type": "platform"})
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
	r.POST("/admin/rbac/roles", func(c *gin.Context) {
		ident := &constant.Identity{UserID: uuid.NewString()}
		ctx := context.WithValue(c.Request.Context(), constant.IdentityKey, ident)
		c.Request = c.Request.WithContext(ctx)
		h.CreateRole(c)
	})

	body, _ := json.Marshal(map[string]any{"code": "admin", "name": "Admin", "scope_type": "platform"})
	req := httptest.NewRequest(http.MethodPost, "/admin/rbac/roles", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 got %d", w.Code)
	}
}
