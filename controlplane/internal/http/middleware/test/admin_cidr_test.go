package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	middleware "controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

func TestAdminCIDRAllowlist(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name      string
		allowlist []string
		clientIP  string
		wantCode  int
	}{
		{
			name:      "empty allowlist disables cidr restriction",
			allowlist: nil,
			clientIP:  "203.0.113.10",
			wantCode:  http.StatusOK,
		},
		{
			name:      "cidr match allows client ip",
			allowlist: []string{"10.0.0.0/8"},
			clientIP:  "10.20.30.40",
			wantCode:  http.StatusOK,
		},
		{
			name:      "exact ip match allows client ip",
			allowlist: []string{"203.0.113.10"},
			clientIP:  "203.0.113.10",
			wantCode:  http.StatusOK,
		},
		{
			name:      "non matching ip is blocked",
			allowlist: []string{"10.0.0.0/8", "203.0.113.10"},
			clientIP:  "198.51.100.5",
			wantCode:  http.StatusForbidden,
		},
		{
			name:      "invalid configured allowlist fails closed",
			allowlist: []string{"not-a-cidr"},
			clientIP:  "203.0.113.10",
			wantCode:  http.StatusForbidden,
		},
		{
			name:      "ipv6 cidr match allows client ip",
			allowlist: []string{"2001:db8::/32"},
			clientIP:  "2001:db8::1",
			wantCode:  http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			middleware.InitAdminCIDR(tt.allowlist)

			engine := gin.New()
			engine.Use(middleware.AdminCIDR())
			engine.GET("/admin", func(c *gin.Context) { c.Status(http.StatusOK) })

			req := httptest.NewRequest(http.MethodGet, "/admin", nil)
			if strings.Contains(tt.clientIP, ":") {
				req.RemoteAddr = "[" + tt.clientIP + "]:12345"
			} else {
				req.RemoteAddr = tt.clientIP + ":12345"
			}
			resp := httptest.NewRecorder()

			engine.ServeHTTP(resp, req)
			if resp.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", resp.Code, tt.wantCode)
			}
		})
	}
}
