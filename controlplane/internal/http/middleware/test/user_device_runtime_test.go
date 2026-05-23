package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controlplane/internal/http/middleware"
	iamCache "controlplane/internal/iam/cache"
	"controlplane/internal/security"
	"controlplane/pkg/constant"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"
)

func setupRequireUserDeviceRuntime(t *testing.T) (iamCache.UserDeviceRuntimeCache, gin.HandlerFunc, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	srv := miniredis.RunT(t)
	rds := goredis.NewClient(&goredis.Options{Addr: srv.Addr()})
	cache := iamCache.NewUserDeviceRuntimeCache(rds)
	guard := middleware.RequireUserDeviceRuntime(cache, 5*time.Second, middleware.UserDeviceCookieScope{})
	return cache, guard, func() { _ = rds.Close() }
}

func TestRequireUserDeviceRuntime_Pass(t *testing.T) {
	cache, guard, cleanup := setupRequireUserDeviceRuntime(t)
	defer cleanup()

	tracking := "track-pass"
	deviceID := "dev-1"
	deviceSecret := "secret-1"
	jti := "jti-1"
	if err := cache.SetDeviceRuntime(context.Background(), iamCache.UserDeviceRuntime{
		TrackingID:       tracking,
		DeviceID:         deviceID,
		DeviceSecretHash: security.HashTokenSHA256(deviceSecret),
		CurrentJTI:       jti,
		TrackedDeviceRef: "tracked-1",
		UserID:           "user-1",
	}, time.Minute); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}

	router := gin.New()
	router.GET("/probe",
		func(c *gin.Context) {
			c.Set(middleware.CtxKeyJTI, jti)
			c.Set(middleware.CtxKeyTrackingID, tracking)
			c.Next()
		},
		guard,
		func(c *gin.Context) { c.Status(http.StatusOK) },
	)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.AddCookie(&http.Cookie{Name: constant.DeviceIDName, Value: deviceID})
	req.AddCookie(&http.Cookie{Name: constant.DeviceSecretName, Value: deviceSecret})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRequireUserDeviceRuntime_Reject(t *testing.T) {
	cache, guard, cleanup := setupRequireUserDeviceRuntime(t)
	defer cleanup()

	tracking := "track-reject"
	if err := cache.SetDeviceRuntime(context.Background(), iamCache.UserDeviceRuntime{
		TrackingID:       tracking,
		DeviceID:         "dev-real",
		DeviceSecretHash: security.HashTokenSHA256("secret-real"),
		CurrentJTI:       "jti-real",
		TrackedDeviceRef: "tracked-r",
		UserID:           "user-r",
	}, time.Minute); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}

	router := gin.New()
	router.GET("/probe",
		func(c *gin.Context) {
			c.Set(middleware.CtxKeyJTI, "jti-real")
			c.Set(middleware.CtxKeyTrackingID, tracking)
			c.Next()
		},
		guard,
		func(c *gin.Context) { c.Status(http.StatusOK) },
	)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.AddCookie(&http.Cookie{Name: constant.DeviceIDName, Value: "dev-fake"})
	req.AddCookie(&http.Cookie{Name: constant.DeviceSecretName, Value: "secret-real"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	// cookie clear: should see Set-Cookie header attempting MaxAge=0 or expires past
	cookies := w.Result().Cookies()
	cleared := 0
	for _, c := range cookies {
		if c.Name == constant.DeviceIDName || c.Name == constant.DeviceSecretName {
			if c.MaxAge < 0 || c.Value == "" {
				cleared++
			}
		}
	}
	if cleared < 2 {
		t.Fatalf("expected device cookies cleared, got %#v", cookies)
	}
}

func TestRequireUserDeviceRuntime_NilRecord(t *testing.T) {
	cache, guard, cleanup := setupRequireUserDeviceRuntime(t)
	defer cleanup()

	router := gin.New()
	router.GET("/probe",
		func(c *gin.Context) {
			c.Set(middleware.CtxKeyJTI, "jti-anything")
			c.Set(middleware.CtxKeyTrackingID, "track-missing")
			c.Next()
		},
		guard,
		func(c *gin.Context) { c.Status(http.StatusOK) },
	)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.AddCookie(&http.Cookie{Name: constant.DeviceIDName, Value: "dev-x"})
	req.AddCookie(&http.Cookie{Name: constant.DeviceSecretName, Value: "secret-x"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	// no DeleteDeviceRuntime call expected -> cache should still report empty for that tracking id.
	stored, err := cache.GetDeviceRuntime(context.Background(), "track-missing")
	if err != nil || stored != nil {
		t.Fatalf("expected no stored runtime, got %#v err=%v", stored, err)
	}
}
