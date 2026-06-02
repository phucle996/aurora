// ============================================================================
// 🛡️ ARCHITECTURAL & SYSTEM CONTRACTS
// ============================================================================
//
// 🤝 1. SYSTEM CONTRACT
//   - Accepted Inputs: Hỗ trợ dải CIDR (ví dụ: "10.0.0.0/8") hoặc IP tĩnh (ví dụ: "203.0.113.10").
//   - Sanitization: Tự động loại bỏ khoảng trắng dư thừa, bỏ qua chuỗi rỗng.
//   - Fail-Closed: Nếu cấu hình không trống nhưng có lỗi cú pháp parse, hệ thống sẽ kích hoạt 
//     bộ lọc với tập luật rỗng để chặn toàn bộ request Admin, tránh lỗ hổng bảo mật.
//
// 📖 2. SOURCE OF TRUTH
//   - Cấu hình gốc từ policy.yaml hoặc DB.
//   - Được app/module.go đọc lúc Bootstrap và gọi InitAdminCIDR một lần để compile 
//     sang cấu trúc dữ liệu RAM.
//   - Router chỉ gọi middleware.AdminCIDR() mà không cần truyền lại tham số.
//
// 🚧 3. SYSTEM BOUNDARY
//   - Middleware hoàn toàn decoupled, không phụ thuộc vào DB, Redis hay service IAM/Core khác.
//   - Chỉ đọc compiled rules trong RAM để đánh giá Client IP.
//
// 💡 4. OPERATIONAL NOTES
//   - Hiệu năng: Parse chuỗi được xử lý trước lúc Bootstrap. Request-time chỉ so khớp O(1) 
//     trên Map và dải CIDR để giữ latency ở mức tối thiểu.
//   - Proxy/NAT: Dựa vào ClientIP của Gin. Trong môi trường Cloud Native / HA, bắt buộc 
//     Reverse Proxy (Nginx, Envoy, Cloudflare) phải truyền đúng header X-Forwarded-For hoặc 
//     CF-Connecting-IP và Gin phải cấu hình Trusted Proxies để tránh IP spoofing.

package middleware

import (
	"net"
	"strings"
	"sync"

	"controlplane/pkg/apires"

	"github.com/gin-gonic/gin"
)

// ============================================================================
// 🔒 TRẠNG THÁI RUNTIME CẤU HÌNH IP ALLOWLIST (GLOBAL ALLOWLIST STATE)
// ============================================================================
var adminCIDRState = struct {
	mu        sync.RWMutex
	allowlist adminCIDRAllowlist
}{}

// adminCIDRAllowlist lưu trữ danh sách các IP/CIDR hợp lệ đã được biên dịch hoàn chỉnh.
type adminCIDRAllowlist struct {
	// enabled = true nghĩa là tệp cấu hình có định nghĩa IP/CIDR (allowlist được kích hoạt).
	// Nếu enabled = true nhưng cả `networks` và `exactSet` đều rỗng (do lỗi cú pháp parse),
	// toàn bộ request sẽ bị chặn. Đây là cơ chế bảo mật Fail-Closed.
	enabled  bool
	networks []*net.IPNet        // Danh sách các dải mạng CIDR (ví dụ: 10.0.0.0/8)
	exactSet map[string]struct{} // Tập hợp các IP tĩnh cụ thể để kiểm tra nhanh O(1) (ví dụ: 192.168.1.5)
}

// ============================================================================
// 🔌 HÀM KHỞI TẠO BỘ LỌC ĐỊA CHỈ IP (INITIALIZATION)
// ============================================================================

// InitAdminCIDR thực hiện biên dịch danh sách địa chỉ IP/dải CIDR hợp lệ sang cấu trúc dữ liệu RAM O(1).
//
// 🎯 LÝ DO TỐI ƯU HÓA HIỆU NĂNG:
//   - Toàn bộ thao tác phân tích chuỗi (string parsing) phức tạp của net.ParseCIDR và net.ParseIP
//     chỉ chạy DUY NHẤT một lần lúc khởi động Server (bootstrap).
//   - Khi có request gửi đến, middleware chỉ cần so khớp O(1) trên Map hoặc duyệt nhanh trên Slice,
//     giúp giữ độ trễ (latency) của middleware ở mức tối thiểu.
func InitAdminCIDR(allowedCIDRs []string) {
	allowlist := compileAdminCIDRAllowlist(allowedCIDRs)
	adminCIDRState.mu.Lock()
	adminCIDRState.allowlist = allowlist
	adminCIDRState.mu.Unlock()
}

// ============================================================================
// 🛡️ MIDDLEWARE: ADMIN CIDR ALLOWLIST (BỘ LỌC ĐỊA CHỈ IP TRUY CẬP ADMIN)
// ============================================================================

// AdminCIDR thực hiện chặn đứng các request truy cập vào API Admin nếu IP của Client không nằm trong Allowlist.
func AdminCIDR() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Nạp bản sao cấu hình một cách an toàn đa luồng:
		adminCIDRState.mu.RLock()
		allowlist := adminCIDRState.allowlist
		adminCIDRState.mu.RUnlock()

		// --------------------------------------------------------------------
		// 🔄 Kiểm tra bộ lọc đã được kích hoạt (enabled) chưa.
		// --------------------------------------------------------------------
		if allowlist.enabled {
			// Phân tích cú pháp IP của Client gửi lên:
			ip := net.ParseIP(strings.TrimSpace(c.ClientIP()))
			allowed := false

			if ip != nil {
				// ------------------------------------------------------------
				// 🔄 Fast-Path: So khớp IP tĩnh cụ thể trong Map O(1).
				// ------------------------------------------------------------
				if allowlist.exactSet != nil {
					if _, ok := allowlist.exactSet[ip.String()]; ok {
						allowed = true
					}
				}

				// ------------------------------------------------------------
				// 🔄 Slow-Path: So khớp IP trong các dải mạng CIDR (O(N)).
				// ------------------------------------------------------------
				if !allowed {
					for _, block := range allowlist.networks {
						if block.Contains(ip) {
							allowed = true
							break // Tìm thấy dải mạng khớp -> Thoát vòng lặp ngay để tối ưu
						}
					}
				}
			}

			// --------------------------------------------------------------------
			// 🔄 Xử lý khi IP không hợp lệ hoặc không có trong allowlist (403 Forbidden).
			// --------------------------------------------------------------------
			if !allowed {
				apires.RespondForbidden(c, "access denied: IP address not in whitelist")
				c.Abort()
				return
			}
		}

		// IP hợp lệ -> Chuyển tiếp request sang middleware/handler tiếp theo
		c.Next()
	}
}

// ============================================================================
// 🛠️ HÀM TRỢ GIÚP NỘI BỘ (HELPER FUNCTION)
// ============================================================================

// compileAdminCIDRAllowlist phân tích mảng các chuỗi IP/CIDR thô thành cấu trúc dữ liệu tối ưu cho việc match IP.
func compileAdminCIDRAllowlist(values []string) adminCIDRAllowlist {
	allowlist := adminCIDRAllowlist{}
	allowlist.exactSet = make(map[string]struct{})

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		// Đánh dấu allowlist đã được bật khi cấu hình không hoàn toàn rỗng:
		allowlist.enabled = true

		// Thử phân tích dạng dải mạng CIDR (ví dụ: 10.0.0.0/8):
		if _, block, err := net.ParseCIDR(value); err == nil {
			allowlist.networks = append(allowlist.networks, block)
			continue
		}

		// Thử phân tích dạng IP tĩnh cụ thể (ví dụ: 192.168.1.100):
		if ip := net.ParseIP(value); ip != nil {
			allowlist.exactSet[ip.String()] = struct{}{}
		}
	}

	// Giải phóng bộ nhớ map nếu không có IP tĩnh nào:
	if len(allowlist.exactSet) == 0 {
		allowlist.exactSet = nil
	}

	return allowlist
}
