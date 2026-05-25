package middleware

import (
	"net"
	"strings"
	"sync"

	"controlplane/pkg/apires"

	"github.com/gin-gonic/gin"
)

var adminCIDRState = struct {
	mu        sync.RWMutex
	allowlist adminCIDRAllowlist
}{}

type adminCIDRAllowlist struct {
	// enabled=false nghĩa là không có config CIDR/IP nào sau khi trim.
	// Nếu enabled=true nhưng rules rỗng, request sẽ bị chặn. Cách này giữ
	// fail-closed cho case config có giá trị nhưng toàn bộ đều invalid.
	enabled  bool
	networks []*net.IPNet
	exactSet map[string]struct{}
}

// InitAdminCIDR khởi tạo allowlist IP/CIDR cho admin routes.
//
// Source of truth:
//   - app/module.go đọc cfg.Security.AdminAllowedCIDRs và gọi hàm này một lần
//     khi dựng module graph.
//   - Route chỉ gọi middleware.AdminCIDR(), không truyền config lặp lại từng route.
//
// Contract:
// - accepted input có thể là CIDR, ví dụ "10.0.0.0/8".
// - accepted input cũng có thể là IP cụ thể, ví dụ "203.0.113.10".
// - chuỗi rỗng bị bỏ qua để config có thể dùng CSV/env var an toàn.
//
// Tối ưu:
// - Parse CIDR/IP ngay tại init, không parse lại trên từng request.
// - Route middleware chỉ đọc compiled rules và so sánh ClientIP.
func InitAdminCIDR(allowedCIDRs []string) {
	allowlist := compileAdminCIDRAllowlist(allowedCIDRs)
	adminCIDRState.mu.Lock()
	adminCIDRState.allowlist = allowlist
	adminCIDRState.mu.Unlock()
}

// AdminCIDR chặn admin request nếu client IP không nằm trong allowlist.
//
// Middleware này cố ý không biết gì về module IAM/Core. Nó chỉ đọc allowlist đã
// init trong package middleware và quyết định pass/block request.
func AdminCIDR() gin.HandlerFunc {
	return func(c *gin.Context) {
		adminCIDRState.mu.RLock()
		allowlist := adminCIDRState.allowlist
		adminCIDRState.mu.RUnlock()

		if allowlist.enabled {
			ip := net.ParseIP(strings.TrimSpace(c.ClientIP()))
			allowed := false
			if ip != nil {
				if allowlist.exactSet != nil {
					if _, ok := allowlist.exactSet[ip.String()]; ok {
						allowed = true
					}
				}
				if !allowed {
					for _, block := range allowlist.networks {
						if block.Contains(ip) {
							allowed = true
							break
						}
					}
				}
			}
			if !allowed {
				apires.RespondForbidden(c, "access denied: IP address not in whitelist")
				c.Abort()
				return
			}
		}

		c.Next()
	}
}

// compileAdminCIDRAllowlist chuẩn hóa config thành rule có thể match trực tiếp.
//
// Invalid CIDR/IP không tạo rule match, nhưng vẫn bật allowlist nếu input không
// rỗng. Nhờ vậy config sai sẽ fail-closed thay vì vô tình mở admin routes.
func compileAdminCIDRAllowlist(values []string) adminCIDRAllowlist {
	allowlist := adminCIDRAllowlist{}
	allowlist.exactSet = make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		allowlist.enabled = true
		if _, block, err := net.ParseCIDR(value); err == nil {
			allowlist.networks = append(allowlist.networks, block)
			continue
		}
		if ip := net.ParseIP(value); ip != nil {
			allowlist.exactSet[ip.String()] = struct{}{}
		}
	}
	if len(allowlist.exactSet) == 0 {
		allowlist.exactSet = nil
	}
	return allowlist
}
