package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/http/middleware"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestAuthorize_PlatformScope(t *testing.T) {
	gin.SetMode(gin.TestMode)

	l1Cache := cacheengine.NewL1Cache()
	registry := cacheengine.NewCacheRegistry(l1Cache)

	userID := uuid.NewString()
	userPerms := []string{
		"platform:iam.users.read",
		"platform:iam.roles.read",
	}

	// Register mock loader
	cacheengine.Register(registry, "rbac:user:permissions", 15*time.Minute, func(ctx context.Context, param string) ([]string, error) {
		return userPerms, nil
	})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		ident := &constant.Identity{
			UserID: userID,
			Role:   "platform_admin",
		}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), constant.IdentityKey, ident))
		c.Next()
	})

	router.GET("/admin/users", middleware.Authorize("platform:iam.users.read", registry), func(c *gin.Context) {
		c.String(http.StatusOK, "allowed")
	})
	router.GET("/admin/write", middleware.Authorize("platform:iam.users.write", registry), func(c *gin.Context) {
		c.String(http.StatusOK, "allowed")
	})

	// 1. Check permitted request
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	router.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w1.Code)
	}

	// 2. Check forbidden request
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/admin/write", nil)
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w2.Code)
	}
}

func TestAuthorize_TenantScope(t *testing.T) {
	gin.SetMode(gin.TestMode)

	l1Cache := cacheengine.NewL1Cache()
	registry := cacheengine.NewCacheRegistry(l1Cache)

	userID := uuid.NewString()
	tenantID := uuid.NewString()
	tenantCode := "tenant-a"

	// Register mock loaders
	cacheengine.Register(registry, "tenant_code_by_id", 1*time.Hour, func(ctx context.Context, param string) (string, error) {
		return tenantCode, nil
	})

	userPerms := []string{
		"tenant-a:hierarchy:tenant-member:delete",
	}
	cacheengine.Register(registry, "rbac:user:permissions", 15*time.Minute, func(ctx context.Context, param string) ([]string, error) {
		return userPerms, nil
	})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		ident := &constant.Identity{
			UserID:   userID,
			Role:     "tenant_admin",
			TenantID: tenantID,
		}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), constant.IdentityKey, ident))
		c.Next()
	})

	router.DELETE("/members", middleware.Authorize("tenant:hierarchy:tenant-member:delete", registry), func(c *gin.Context) {
		c.String(http.StatusOK, "deleted")
	})

	// 1) Test with custom x-tenant-code header directly
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodDelete, "/members", nil)
	req1.Header.Set("x-tenant-code", "tenant-a")
	router.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w1.Code)
	}

	// 2) Test resolving from x-tenant-id header
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodDelete, "/members", nil)
	req2.Header.Set("x-tenant-id", tenantID)
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}

	// 3) Test resolving from fallback Identity Context TenantID
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodDelete, "/members", nil)
	router.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w3.Code)
	}

	// 4) Test wrong tenant context (should be forbidden)
	w4 := httptest.NewRecorder()
	req4 := httptest.NewRequest(http.MethodDelete, "/members", nil)
	req4.Header.Set("x-tenant-code", "tenant-b")
	router.ServeHTTP(w4, req4)
	if w4.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w4.Code)
	}
}

func TestAuthorize_PersonalScope(t *testing.T) {
	gin.SetMode(gin.TestMode)

	l1Cache := cacheengine.NewL1Cache()
	registry := cacheengine.NewCacheRegistry(l1Cache)

	userID := uuid.NewString()
	userPerms := []string{
		"personal:vps:vps:create",
	}

	cacheengine.Register(registry, "rbac:user:permissions", 15*time.Minute, func(ctx context.Context, param string) ([]string, error) {
		return userPerms, nil
	})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		ident := &constant.Identity{
			UserID: userID,
			Role:   "platform_user",
		}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), constant.IdentityKey, ident))
		c.Next()
	})

	router.POST("/vps", middleware.Authorize("personal:vps:vps:create", registry), func(c *gin.Context) {
		c.String(http.StatusOK, "created")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/vps", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAuthorize_PlatformRootBypass(t *testing.T) {
	gin.SetMode(gin.TestMode)

	l1Cache := cacheengine.NewL1Cache()
	registry := cacheengine.NewCacheRegistry(l1Cache)

	userID := uuid.NewString()

	router := gin.New()
	router.Use(func(c *gin.Context) {
		ident := &constant.Identity{
			UserID: userID,
			Role:   "platform_root", // Root role bypass
		}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), constant.IdentityKey, ident))
		c.Next()
	})

	router.GET("/restricted", middleware.Authorize("platform:some.critical.permission", registry), func(c *gin.Context) {
		c.String(http.StatusOK, "bypassed")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/restricted", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
