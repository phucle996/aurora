package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/http/middleware"
	"controlplane/internal/security"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestZoneAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 1. Setup CacheRegistry và đăng ký loader "zone_by_code"
	cache := cacheengine.NewShardedCache()
	registry := cacheengine.NewCacheRegistry(cache)

	zoneMap := map[string]string{
		"vn-hn-1":  "94b29bb8-7ad3-4bde-a39c-29b1be3855a8",
		"vn-sg-1":  "12c6a858-a579-450e-a61f-d31e9c20a4b0",
		"us-east":  "f8664ea6-2187-43cf-bf2f-04664a7852a3",
	}

	cacheengine.Register(registry, "zone_by_code", time.Minute, func(ctx context.Context, param string) (string, error) {
		id, ok := zoneMap[param]
		if !ok {
			return "", errors.New("zone not found")
		}
		return id, nil
	})

	middleware.InitZoneAuth(registry)

	t.Run("AdminZoneAuth - Global Admin - Access Global", func(t *testing.T) {
		router := gin.New()
		router.GET("/test",
			func(c *gin.Context) {
				// Giả lập token admin không bị giới hạn zone
				c.Set(constant.ContextKeyAdminZoneID, "")
				c.Next()
			},
			middleware.AdminZoneAuth(),
			func(c *gin.Context) {
				id, ok := middleware.GetZoneID(c.Request.Context())
				if !ok || id != uuid.Nil {
					t.Errorf("expected global zone ID to be uuid.Nil, got %v (ok: %t)", id, ok)
				}
				c.Status(http.StatusOK)
			},
		)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})

	t.Run("AdminZoneAuth - Global Admin - Access Specific Zone", func(t *testing.T) {
		router := gin.New()
		router.GET("/test",
			func(c *gin.Context) {
				c.Set(constant.ContextKeyAdminZoneID, "")
				c.Next()
			},
			middleware.AdminZoneAuth(),
			func(c *gin.Context) {
				id, ok := middleware.GetZoneID(c.Request.Context())
				expectedID := uuid.MustParse(zoneMap["vn-hn-1"])
				if !ok || id != expectedID {
					t.Errorf("expected zone ID to be %v, got %v (ok: %t)", expectedID, id, ok)
				}
				c.Status(http.StatusOK)
			},
		)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.AddCookie(&http.Cookie{Name: constant.ZoneCodeName, Value: "vn-hn-1"})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})

	t.Run("AdminZoneAuth - Restricted Admin - Matching Zone Access", func(t *testing.T) {
		router := gin.New()
		router.GET("/test",
			func(c *gin.Context) {
				c.Set(constant.ContextKeyAdminZoneID, zoneMap["vn-hn-1"])
				c.Next()
			},
			middleware.AdminZoneAuth(),
			func(c *gin.Context) {
				c.Status(http.StatusOK)
			},
		)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.AddCookie(&http.Cookie{Name: constant.ZoneCodeName, Value: "vn-hn-1"})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})

	t.Run("AdminZoneAuth - Restricted Admin - Mismatched Zone Access", func(t *testing.T) {
		router := gin.New()
		router.GET("/test",
			func(c *gin.Context) {
				c.Set(constant.ContextKeyAdminZoneID, zoneMap["vn-hn-1"])
				c.Next()
			},
			middleware.AdminZoneAuth(),
			func(c *gin.Context) {
				c.Status(http.StatusOK)
			},
		)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.AddCookie(&http.Cookie{Name: constant.ZoneCodeName, Value: "vn-sg-1"})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden, got %d", w.Code)
		}
	})

	t.Run("AdminZoneAuth - Restricted Admin - Denies Global Access", func(t *testing.T) {
		router := gin.New()
		router.GET("/test",
			func(c *gin.Context) {
				c.Set(constant.ContextKeyAdminZoneID, zoneMap["vn-hn-1"])
				c.Next()
			},
			middleware.AdminZoneAuth(),
			func(c *gin.Context) {
				c.Status(http.StatusOK)
			},
		)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		// Không truyền header/cookie zone_code -> defaults to "global"
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden, got %d", w.Code)
		}
	})

	t.Run("AdminZoneAuth - Invalid Zone Code", func(t *testing.T) {
		router := gin.New()
		router.GET("/test",
			func(c *gin.Context) {
				c.Set(constant.ContextKeyAdminZoneID, "")
				c.Next()
			},
			middleware.AdminZoneAuth(),
			func(c *gin.Context) {
				c.Status(http.StatusOK)
			},
		)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.AddCookie(&http.Cookie{Name: constant.ZoneCodeName, Value: "vn-invalid"})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request, got %d", w.Code)
		}
	})

	t.Run("UserZoneAuth - Matching Zone Access", func(t *testing.T) {
		router := gin.New()
		router.GET("/test",
			func(c *gin.Context) {
				c.Set(constant.ContextKeyJWTClaims, security.Claims{
					ZoneID: zoneMap["us-east"],
				})
				c.Next()
			},
			middleware.UserZoneAuth(),
			func(c *gin.Context) {
				id, ok := middleware.GetZoneID(c.Request.Context())
				expectedID := uuid.MustParse(zoneMap["us-east"])
				if !ok || id != expectedID {
					t.Errorf("expected user zone ID, got %v (ok: %t)", id, ok)
				}
				c.Status(http.StatusOK)
			},
		)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.AddCookie(&http.Cookie{Name: constant.ZoneCodeName, Value: "us-east"})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})

	t.Run("UserZoneAuth - Mismatched Zone Access", func(t *testing.T) {
		router := gin.New()
		router.GET("/test",
			func(c *gin.Context) {
				c.Set(constant.ContextKeyJWTClaims, security.Claims{
					ZoneID: zoneMap["vn-hn-1"],
				})
				c.Next()
			},
			middleware.UserZoneAuth(),
			func(c *gin.Context) {
				c.Status(http.StatusOK)
			},
		)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.AddCookie(&http.Cookie{Name: constant.ZoneCodeName, Value: "us-east"})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden, got %d", w.Code)
		}
	})

	t.Run("UserZoneAuth - Rejects Global Access", func(t *testing.T) {
		router := gin.New()
		router.GET("/test",
			func(c *gin.Context) {
				c.Set(constant.ContextKeyJWTClaims, security.Claims{
					ZoneID: zoneMap["vn-hn-1"],
				})
				c.Next()
			},
			middleware.UserZoneAuth(),
			func(c *gin.Context) {
				c.Status(http.StatusOK)
			},
		)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.AddCookie(&http.Cookie{Name: constant.ZoneCodeName, Value: "global"})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request, got %d", w.Code)
		}
	})

	t.Run("UserZoneAuth - Rejects Missing Zone Code", func(t *testing.T) {
		router := gin.New()
		router.GET("/test",
			func(c *gin.Context) {
				c.Set(constant.ContextKeyJWTClaims, security.Claims{
					ZoneID: zoneMap["vn-hn-1"],
				})
				c.Next()
			},
			middleware.UserZoneAuth(),
			func(c *gin.Context) {
				c.Status(http.StatusOK)
			},
		)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request, got %d", w.Code)
		}
	})
}
