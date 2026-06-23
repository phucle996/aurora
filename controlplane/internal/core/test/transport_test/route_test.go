package transport_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"controlplane/internal/core"
	coreHandler "controlplane/internal/core/transport/http/handler"
	"controlplane/internal/http/middleware"
	"controlplane/internal/cacheengine"

	"github.com/gin-gonic/gin"
)

func TestRegisterRoutesAppliesAdminAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = middleware.InitAdminAPIKeyAuth(cacheengine.NewCacheRegistry(cacheengine.NewL1Cache()))

	r := gin.New()
	called := false
	r.Use(func(c *gin.Context) {
		called = true
		c.Next()
	})
	core.RegisterRoutes(
		r,
		&core.Module{ZoneHandler: coreHandler.NewZoneHandler(&zoneServiceStub{})},
	)

	req := httptest.NewRequest(http.MethodGet, "/admin/core/zones", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !called {
		t.Fatal("expected route to be registered")
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected admin auth middleware response, got %d", w.Code)
	}
}
