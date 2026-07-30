// ============================================================================
// 🛡️ ARCHITECTURAL & SYSTEM CONTRACTS
// ============================================================================
//
// 🤝 1. SYSTEM CONTRACT
//   - Structured Logging: Phát hiện và ghi nhận thông tin request (Method, Path, Status, Latency, IP)
//     vào hệ thống log tập trung sau khi request hoàn tất xử lý.
//   - Error Classification: Phân loại mã lỗi nghiệp vụ (request_error) hoặc lỗi hệ thống (http_error)
//     dựa trên Gin context errors và HTTP status code để phục vụ dashboard giám sát.
//
// 📖 2. SOURCE OF TRUTH
//   - logger.AccessLog là Source of Truth để in log JSON trực tiếp ra Standard Error (Stderr).
//
// 🚧 3. SYSTEM BOUNDARY
//   - Hoàn toàn độc lập, là lớp giám sát (Observability Layer) không can thiệp vào logic xử lý nghiệp vụ
//     hoặc sửa đổi payload của request/response.
//
// 💡 4. OPERATIONAL NOTES
//   - Vị trí kích hoạt: Nên được đặt ở đầu chuỗi middleware của Router để tính toán chính xác tổng
//     thời gian xử lý (end-to-end latency) bao gồm cả thời gian chạy qua các middleware bảo mật khác.
//   - Khôi phục lỗi: Phân tách chi tiết IP thực tế của client qua cơ chế Proxy header.

package middleware

import (
	"net"
	"strings"
	"time"

	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
)

// AccessLog ghi nhận nhật ký truy cập (access log) có cấu trúc sau khi request hoàn tất.
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		// --------------------------------------------------------------------
		// 🔄 Ghi nhận mốc thời gian bắt đầu xử lý request.
		// --------------------------------------------------------------------
		start := time.Now()

		// Chuyển tiếp xử lý sang các middleware và handler tiếp theo trong chuỗi
		c.Next()

		// --------------------------------------------------------------------
		// 🔄 Lấy route template đã đăng ký; raw path do client kiểm soát không
		// được dùng làm correlation key hoặc log stream dimension.
		// --------------------------------------------------------------------
		route := strings.TrimSpace(c.FullPath())
		if route == "" {
			route = "__unmatched__"
		}

		// --------------------------------------------------------------------
		// 🔄 Tính toán độ trễ (latencyMs) và trích xuất mã HTTP Status Code.
		// --------------------------------------------------------------------
		statusCode := c.Writer.Status()
		latencyMs := float64(time.Since(start)) / float64(time.Millisecond)
		errorCode := ""

		// --------------------------------------------------------------------
		// 🔄 Phân loại lỗi dựa trên trạng thái Gin errors hoặc Status Code.
		// --------------------------------------------------------------------
		if len(c.Errors) > 0 {
			errorCode = "request_error"
		}

		if statusCode >= 500 && errorCode == "" {
			errorCode = "http_error"
		}

		// --------------------------------------------------------------------
		// 🔄 Đẩy bản ghi access log hoàn chỉnh sang tầng logger chuyên dụng.
		// --------------------------------------------------------------------
		logger.AccessLog(
			c,
			"",
			errorCode,
			requestMethod(c),
			route,
			statusCode,
			latencyMs,
			requestIP(c),
		)
	}
}

// requestMethod trích xuất phương thức HTTP của request.
func requestMethod(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	return strings.TrimSpace(c.Request.Method)
}

// requestIP giải quyết địa chỉ IP chính xác của Client, tự động fallback nếu cần.
func requestIP(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}

	// Ưu tiên đọc IP đã được xử lý qua Trusted Proxies headers:
	if ip := strings.TrimSpace(c.ClientIP()); ip != "" {
		return ip
	}

	// Nếu không có proxy headers, fallback về Remote Address trực tiếp của TCP socket:
	addr := strings.TrimSpace(c.Request.RemoteAddr)
	if addr == "" {
		return ""
	}

	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return strings.TrimSpace(host)
	}

	return addr
}
