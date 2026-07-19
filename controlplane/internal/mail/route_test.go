package mail

import (
	"strings"
	"testing"

	mailHandler "controlplane/internal/mail/transport/http/handler"

	"github.com/gin-gonic/gin"
)

func TestRegisterRoutesUsesRewrittenPersonalAndTenantPrefixes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	module := &Module{
		PersonalConsumerHandler: mailHandler.NewPersonalConsumerHandler(nil),
		TenantConsumerHandler:   mailHandler.NewTenantConsumerHandler(nil),
		PersonalTemplateHandler: mailHandler.NewPersonalTemplateHandler(nil),
		TenantTemplateHandler:   mailHandler.NewTenantTemplateHandler(nil),
	}
	RegisterRoutes(router, module)

	routes := router.Routes()
	if len(routes) != 26 {
		t.Fatalf("route count = %d, want 26", len(routes))
	}
	for _, route := range routes {
		if route.Path == "/api/v1/mail" {
			t.Fatalf("legacy or malformed route registered: %s %s", route.Method, route.Path)
		}
		if !strings.HasPrefix(route.Path, "/api/v1/personal/mail") &&
			!strings.HasPrefix(route.Path, "/api/v1/tenant/mail") {
			t.Fatalf("route is outside rewritten scope: %s %s", route.Method, route.Path)
		}
	}
}
