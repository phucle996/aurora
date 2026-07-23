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
		InfrastructureHandler:   mailHandler.NewInfrastructureHandler(nil),
	}
	RegisterRoutes(router, module)

	routes := router.Routes()
	if len(routes) != 27 {
		t.Fatalf("route count = %d, want 27", len(routes))
	}
	for _, route := range routes {
		if route.Path == "/api/v1/mail" {
			t.Fatalf("legacy or malformed route registered: %s %s", route.Method, route.Path)
		}
		// [COMMENT]: Kiểm tra đường dẫn route admin infrastructure khớp với định dạng mới không còn route param
		if !strings.HasPrefix(route.Path, "/api/v1/personal/mail") &&
			!strings.HasPrefix(route.Path, "/api/v1/tenant/mail") &&
			route.Path != "/admin/mail/infrastructure" {
			t.Fatalf("route is outside rewritten scope: %s %s", route.Method, route.Path)
		}
	}
}
