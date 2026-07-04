package transport_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	handler "controlplane/internal/iam/transport/http/handler"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type rbacServiceStub struct {
	getUserRolePermissionsFn func(ctx context.Context, userID uuid.UUID, workspaceID uuid.UUID) ([]byte, error)
	assignUserRoleFn         func(ctx context.Context, userRole *iamEntity.UserRole) error
	assignTenantRoleFn       func(ctx context.Context, tenantRole *iamEntity.TenantRole) error
	listPlatformRolesFn      func(ctx context.Context) ([]iamEntity.Role, error)
	listTenantRolesFn        func(ctx context.Context, tenantID uuid.UUID) ([]iamEntity.Role, error)
}

type testRoleResponse struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	RoleLevel int    `json:"role_level"`
	Scope     string `json:"scope"`
}

var _ iamSvcInterface.RbacService = (*rbacServiceStub)(nil)

func (s *rbacServiceStub) GetUserRolePermissions(ctx context.Context, userID uuid.UUID, workspaceID uuid.UUID) ([]byte, error) {
	if s.getUserRolePermissionsFn != nil {
		return s.getUserRolePermissionsFn(ctx, userID, workspaceID)
	}
	return nil, nil
}

func (s *rbacServiceStub) AssignUserRole(ctx context.Context, userRole *iamEntity.UserRole) error {
	if s.assignUserRoleFn != nil {
		return s.assignUserRoleFn(ctx, userRole)
	}
	return nil
}

func (s *rbacServiceStub) AssignTenantRole(ctx context.Context, tenantRole *iamEntity.TenantRole) error {
	if s.assignTenantRoleFn != nil {
		return s.assignTenantRoleFn(ctx, tenantRole)
	}
	return nil
}

func (s *rbacServiceStub) ListPlatformRoles(ctx context.Context) ([]iamEntity.Role, error) {
	if s.listPlatformRolesFn != nil {
		return s.listPlatformRolesFn(ctx)
	}
	return nil, nil
}

func (s *rbacServiceStub) ListTenantRoles(ctx context.Context, tenantID uuid.UUID) ([]iamEntity.Role, error) {
	if s.listTenantRolesFn != nil {
		return s.listTenantRolesFn(ctx, tenantID)
	}
	return nil, nil
}

func TestRbacHandler_ListPlatformRoles_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	mockRoles := []iamEntity.Role{
		{
			ID:        uuid.New(),
			Code:      "platform_admin",
			Name:      "System Admin",
			RoleLevel: 1,
			Scope:     "platform",
		},
	}

	h := handler.NewRbacHandler(&rbacServiceStub{
		listPlatformRolesFn: func(ctx context.Context) ([]iamEntity.Role, error) {
			return mockRoles, nil
		},
	})

	r.GET("/api/v1/iam/rbac/role", h.ListPlatformRoles)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/iam/rbac/role", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}

	var result struct {
		Message string `json:"message"`
		Data    struct {
			Roles []testRoleResponse `json:"roles"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unexpected body parse error: %v", err)
	}
	resp := result.Data.Roles

	if len(resp) != 1 || resp[0].Code != "platform_admin" {
		t.Errorf("unexpected body payload: %+v", resp)
	}
}

func TestRbacHandler_ListPlatformRoles_Error(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	h := handler.NewRbacHandler(&rbacServiceStub{
		listPlatformRolesFn: func(ctx context.Context) ([]iamEntity.Role, error) {
			return nil, errors.New("database unavailable")
		},
	})

	r.GET("/api/v1/iam/rbac/role", h.ListPlatformRoles)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/iam/rbac/role", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 got %d", w.Code)
	}
}

func TestRbacHandler_ListTenantRoles_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

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

	h := handler.NewRbacHandler(&rbacServiceStub{
		listTenantRolesFn: func(ctx context.Context, tid uuid.UUID) ([]iamEntity.Role, error) {
			if tid != tenantID {
				return nil, errors.New("unexpected tenant id")
			}
			return mockRoles, nil
		},
	})

	r.GET("/api/v1/iam/rbac/role/tenant", h.ListTenantRoles)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/iam/rbac/role/tenant", nil)
	req.Header.Set("X-Tenant-ID", tenantID.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}

	var result struct {
		Message string `json:"message"`
		Data    struct {
			Roles []testRoleResponse `json:"roles"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unexpected body parse error: %v", err)
	}
	resp := result.Data.Roles

	if len(resp) != 1 || resp[0].Code != "tenant_admin" {
		t.Errorf("unexpected body payload: %+v", resp)
	}
}

func TestRbacHandler_ListTenantRoles_MissingTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	h := handler.NewRbacHandler(&rbacServiceStub{})

	r.GET("/api/v1/iam/rbac/role/tenant", h.ListTenantRoles)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/iam/rbac/role/tenant", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 got %d", w.Code)
	}
}

func TestRbacHandler_ListTenantRoles_Error(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	tenantID := uuid.New()
	h := handler.NewRbacHandler(&rbacServiceStub{
		listTenantRolesFn: func(ctx context.Context, tid uuid.UUID) ([]iamEntity.Role, error) {
			return nil, errors.New("db lookup error")
		},
	})

	r.GET("/api/v1/iam/rbac/role/tenant", h.ListTenantRoles)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/iam/rbac/role/tenant", nil)
	req.Header.Set("X-Tenant-ID", tenantID.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 got %d", w.Code)
	}
}
