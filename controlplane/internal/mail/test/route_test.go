package mail_test

import (
	mail "controlplane/internal/mail"
	"strings"
	"testing"

	mailHandler "controlplane/internal/mail/transport/http/handler"

	"github.com/gin-gonic/gin"
)

func TestRegisterRoutesUsesRewrittenPersonalAndTenantPrefixes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	module := &mail.Module{
		PersonalConsumerHandler: mailHandler.NewPersonalConsumerHandler(nil),
		TenantConsumerHandler:   mailHandler.NewTenantConsumerHandler(nil),
		PersonalTemplateHandler: mailHandler.NewPersonalTemplateHandler(nil),
		TenantTemplateHandler:   mailHandler.NewTenantTemplateHandler(nil),
	}
	mail.RegisterRoutes(router, module)

	routes := router.Routes()
	if len(routes) != 28 {
		t.Fatalf("route count = %d, want 28", len(routes))
	}
	for _, route := range routes {
		if route.Path == "/api/v1/mail" {
			t.Fatalf("legacy or malformed route registered: %s %s", route.Method, route.Path)
		}
		if !strings.HasPrefix(route.Path, "/api/v1/personal/mail") &&
			!strings.HasPrefix(route.Path, "/api/v1/tenant/mail") &&
			!strings.HasPrefix(route.Path, "/api/v1/personal/critical/mail") &&
			!strings.HasPrefix(route.Path, "/api/v1/tenant/critical/mail") {
			t.Fatalf("route is outside rewritten scope: %s %s", route.Method, route.Path)
		}
	}
}
