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
	ips      []net.IP
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
		allowlist := getAdminCIDRAllowlist()
		if !isIPAllowed(c.ClientIP(), allowlist) {
			apires.RespondForbidden(c, "access denied: IP address not in whitelist")
			c.Abort()
			return
		}
		c.Next()
	}
}

// getAdminCIDRAllowlist copy struct header dưới read lock.
//
// Compiled rules là immutable sau InitAdminCIDR. Nếu sau này có config reload và
// InitAdminCIDR được gọi lại, state chỉ được replace bằng allowlist mới, không
// mutate allowlist cũ. Vì vậy request đang chạy có thể dùng snapshot này an toàn
// mà không cần allocate copy slice trên từng request.
func getAdminCIDRAllowlist() adminCIDRAllowlist {
	adminCIDRState.mu.RLock()
	allowlist := adminCIDRState.allowlist
	adminCIDRState.mu.RUnlock()
	return allowlist
}

// compileAdminCIDRAllowlist chuẩn hóa config thành rule có thể match trực tiếp.
//
// Invalid CIDR/IP không tạo rule match, nhưng vẫn bật allowlist nếu input không
// rỗng. Nhờ vậy config sai sẽ fail-closed thay vì vô tình mở admin routes.
func compileAdminCIDRAllowlist(values []string) adminCIDRAllowlist {
	allowlist := adminCIDRAllowlist{}
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
			allowlist.ips = append(allowlist.ips, ip)
		}
	}
	return allowlist
}

// isIPAllowed kiểm tra IP client theo allowlist.
//
// Empty allowlist nghĩa là không bật IP restriction. Config mặc định có thể
// dùng 0.0.0.0/0 hoặc ::/0, nhưng empty list vẫn được xem là allow-all để tránh
// fail-close ngoài ý muốn khi môi trường chưa cấu hình CIDR.
func isIPAllowed(ipText string, allowlist adminCIDRAllowlist) bool {
	if !allowlist.enabled {
		return true
	}
	ip := net.ParseIP(strings.TrimSpace(ipText))
	if ip == nil {
		return false
	}
	for _, block := range allowlist.networks {
		if block.Contains(ip) {
			return true
		}
	}
	for _, exact := range allowlist.ips {
		if exact.Equal(ip) {
			return true
		}
	}
	return false
}
