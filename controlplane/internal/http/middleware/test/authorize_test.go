package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/http/middleware"
	iamproto "controlplane/internal/iam/transport/proto"
	"controlplane/internal/observability"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// makeRoleCache tạo L1 cache với mock loader cho membership_role và user_role trả về perms cố định.
func makeRoleCache(perms []string) *cacheengine.CacheRegistry {
	l1Cache := cacheengine.NewL1Cache()
	registry := cacheengine.NewCacheRegistry(l1Cache, observability.NewNoopCacheRecorder())
	cacheengine.Register(registry, "membership_role", 15*time.Minute, func(ctx context.Context, param string) (*iamproto.RoleEntry, error) {
		return &iamproto.RoleEntry{Permissions: perms}, nil
	})
	cacheengine.Register(registry, "user_role", 15*time.Minute, func(ctx context.Context, param string) (*iamproto.RoleEntry, error) {
		return &iamproto.RoleEntry{Permissions: perms}, nil
	})
	return registry
}

// TestAuthorize_TenantScope kiểm tra nhánh Tenant: X-Tenant-ID có → cấp 1 = tenant_uuid.
func TestAuthorize_TenantScope(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tenantID := uuid.NewString()
	workspaceID := uuid.NewString()
	userID := uuid.NewString()

	// [COMMENT]: DB đã lưu sẵn full 5-part key với tenant_uuid và workspace_uuid thực tế
	expectedPerm := tenantID + ":" + workspaceID + ":hierarchy:tenant-member:delete"
	registry := makeRoleCache([]string{expectedPerm})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request.Header.Set("X-User-ID", userID)
		c.Request.Header.Set("X-Tenant-ID", tenantID)
		c.Request.Header.Set("X-Workspace-ID", workspaceID)
		c.Next()
	})
	// [COMMENT]: Production route luôn chạy ContextInjector trước Authorize; test phải giữ cùng middleware order.
	router.Use(middleware.ContextInjector())

	router.DELETE("/members",
		middleware.Authorize("hierarchy:tenant-member:delete", registry, "*"),
		func(c *gin.Context) { c.String(http.StatusOK, "deleted") },
	)
	router.DELETE("/roles",
		middleware.Authorize("iam:role:delete", registry, "*"),
		func(c *gin.Context) { c.String(http.StatusOK, "deleted") },
	)

	// 1. Có quyền → 200
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodDelete, "/members", nil)
	router.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w1.Code)
	}

	// 2. Không có quyền → 403
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodDelete, "/roles", nil)
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w2.Code)
	}
}

// TestAuthorize_PersonalScope kiểm tra nhánh Personal: X-Tenant-ID vắng mặt → cấp 1 = username.
func TestAuthorize_PersonalScope(t *testing.T) {
	gin.SetMode(gin.TestMode)

	username := "alice"
	workspaceID := uuid.NewString()
	userID := uuid.NewString()

	// [COMMENT]: DB đã lưu sẵn full 5-part key với username và workspace_uuid thực tế
	expectedPerm := username + ":" + workspaceID + ":hypervisor:vps:create"
	registry := makeRoleCache([]string{expectedPerm})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request.Header.Set("X-User-ID", userID)
		c.Request.Header.Set("X-User-Name", username)
		c.Request.Header.Set("X-Workspace-ID", workspaceID)
		// [COMMENT]: Không set X-Tenant-ID → middleware đi nhánh personal
		c.Next()
	})
	router.Use(middleware.ContextInjector())

	router.POST("/vps",
		middleware.Authorize("hypervisor:vps:create", registry, "*"),
		func(c *gin.Context) { c.String(http.StatusOK, "created") },
	)
	router.DELETE("/vps",
		middleware.Authorize("hypervisor:vps:delete", registry, "*"),
		func(c *gin.Context) { c.String(http.StatusOK, "deleted") },
	)

	// 1. Có quyền → 200
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/vps", nil)
	router.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w1.Code)
	}

	// 2. Không có quyền → 403
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodDelete, "/vps", nil)
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w2.Code)
	}
}

// TestAuthorize_MissingWorkspaceID kiểm tra thiếu workspace context → 403.
func TestAuthorize_MissingWorkspaceID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	registry := makeRoleCache([]string{"some-perm"})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request.Header.Set("X-User-ID", uuid.NewString())
		c.Request.Header.Set("X-Tenant-ID", uuid.NewString())
		// [COMMENT]: Không set X-Workspace-ID → phải reject
		c.Next()
	})
	router.Use(middleware.ContextInjector())

	router.GET("/test",
		middleware.Authorize("iam:role:read", registry, "*"),
		func(c *gin.Context) { c.String(http.StatusOK, "ok") },
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

// TestAuthorize_UserLevelChecking kiểm tra logic xác thực hierarchy level của Actor
func TestAuthorize_UserLevelChecking(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tenantID := uuid.NewString()
	workspaceID := uuid.NewString()
	userID := uuid.NewString()

	expectedPerm := tenantID + ":" + workspaceID + ":hierarchy:tenant-member:delete"
	registry := makeRoleCache([]string{expectedPerm})

	// Thiết lập router với middleware gán headers động
	setupRouter := func(level string) *gin.Engine {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Request.Header.Set("X-User-ID", userID)
			c.Request.Header.Set("X-Tenant-ID", tenantID)
			c.Request.Header.Set("X-Workspace-ID", workspaceID)
			if level != "" {
				c.Request.Header.Set("X-User-Level", level)
			}
			c.Next()
		})
		r.Use(middleware.ContextInjector())
		return r
	}

	// 1. Actor level 1 <= requiredLevel 2 → Cho phép (200)
	r1 := setupRouter("1")
	r1.DELETE("/members",
		middleware.Authorize("hierarchy:tenant-member:delete", registry, "2"),
		func(c *gin.Context) { c.String(http.StatusOK, "deleted") },
	)
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodDelete, "/members", nil)
	r1.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w1.Code)
	}

	// 2. Actor level 8 > requiredLevel 2 → Bị chặn (403)
	r2 := setupRouter("8")
	r2.DELETE("/members",
		middleware.Authorize("hierarchy:tenant-member:delete", registry, "2"),
		func(c *gin.Context) { c.String(http.StatusOK, "deleted") },
	)
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodDelete, "/members", nil)
	r2.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w2.Code)
	}

	// 3. Thiếu header user level context → Bị chặn (403)
	r3 := setupRouter("")
	r3.DELETE("/members",
		middleware.Authorize("hierarchy:tenant-member:delete", registry, "2"),
		func(c *gin.Context) { c.String(http.StatusOK, "deleted") },
	)
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodDelete, "/members", nil)
	r3.ServeHTTP(w3, req3)
	if w3.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w3.Code)
	}
}
