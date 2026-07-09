// ============================================================================
// 🛡️ ARCHITECTURAL & SYSTEM CONTRACTS
// ============================================================================
//
// 🤝 1. SYSTEM CONTRACT
//   - CORS Management: Thiết lập các cấu hình chia sẻ tài nguyên đa nguồn (Cross-Origin Resource Sharing).
//   - Allowed Headers: Hỗ trợ đầy đủ các headers tùy chỉnh bảo mật của hệ thống như X-Request-ID,
//     X-Client-Device-ID, X-Admin-Signature, X-Admin-Timestamp, X-Admin-Nonce, X-Admin-StepUp-Code.
//   - Allowed Credentials: Bật Access-Control-Allow-Credentials để hỗ trợ các phiên xác thực qua cookie.
//
// 📖 2. SOURCE OF TRUTH
//   - Danh sách allowedOrigins được cấu hình động từ biến môi trường và nạp vào duy nhất một lần
//     trong quá trình khởi tạo ứng dụng.
//
// 🚧 3. SYSTEM BOUNDARY
//   - Trong môi trường Cloud Native và Highly Available (HA), CORS nên được cấu hình trực tiếp tại Edge Proxy
//     (Envoy Edge Gateway) để đạt tối đa hiệu năng. CORS middleware này đóng vai trò dự phòng (Fallback)
//     an toàn cho ứng dụng khi chạy local hoặc test độc lập.
//
// 💡 4. OPERATIONAL NOTES
//   - Phân tích tĩnh: Chuyển đổi danh sách cho phép (allowedOrigins) thành cấu trúc Map O(1) tại thời điểm
//     bootstrap để tối ưu hóa tốc độ kiểm tra lúc runtime.
//   - Preflight Termination: Nhận biết và phản hồi sớm (Fast-path termination) các request OPTIONS preflight
//     với mã trạng thái 204 No Content.

package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORS khởi tạo middleware điều phối chia sẻ tài nguyên đa nguồn (CORS).
func CORS(allowedOrigins []string) gin.HandlerFunc {
	// Khởi tạo Map O(1) chứa danh sách các Origin được phép truy cập:
	origins := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		origins[strings.ToLower(strings.TrimSpace(o))] = struct{}{}
	}

	return func(c *gin.Context) {
		origin := strings.ToLower(strings.TrimSpace(c.GetHeader("Origin")))
		if origin == "" {
			c.Next()
			return
		}

		// --------------------------------------------------------------------
		// 🔄 Kiểm tra xem Origin hiện tại có nằm trong Allowlist không.
		// --------------------------------------------------------------------
		_, allowed := origins[origin]
		if !allowed {
			// Cơ chế dự phòng: Tự động cho phép nếu Origin khớp phần đuôi (suffix) với Host hiện tại (Same-Origin).
			if strings.HasSuffix(origin, c.Request.Host) {
				allowed = true
			}
		}

		// --------------------------------------------------------------------
		// 🔄 Thiết lập CORS Headers nếu Origin được xác nhận hợp lệ.
		// --------------------------------------------------------------------
		if allowed {
			c.Header("Access-Control-Allow-Origin", c.GetHeader("Origin"))
			c.Header("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE, PATCH")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-Request-ID, X-Client-Device-ID, X-Admin-Signature, X-Admin-Timestamp, X-Admin-Nonce, X-Admin-StepUp-Code")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Max-Age", "86400")
		}

		// --------------------------------------------------------------------
		// 🔄 Đánh chặn sớm và kết thúc preflight request (OPTIONS) tức thời.
		// --------------------------------------------------------------------
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
