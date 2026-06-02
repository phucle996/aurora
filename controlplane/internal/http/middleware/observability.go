// ============================================================================
// 🛡️ ARCHITECTURAL & SYSTEM CONTRACTS
// ============================================================================
//
// 🤝 1. SYSTEM CONTRACT
//   - Tracing Integration: Kế thừa và phân phối định danh giao dịch (traceparent và X-Request-ID)
//     từ Envoy Edge Proxy xuyên suốt qua Controlplane và Dataplane.
//   - Visibility Rules: traceparent là tài sản nội bộ không trả về Client để tránh rò rỉ thông tin hạ tầng.
//     X-Request-ID được phản hồi về Client để hỗ trợ đối chiếu log khi xảy ra lỗi.
//   - Fail-Open: Telemetry là phụ trợ, Business Logic là chính. Nếu các công cụ giám sát (Tempo, Prometheus)
//     gặp sự cố, middleware cam kết chạy Fail-Open (không chặn hay báo lỗi 500), tự sinh ID dự phòng để
//     bảo vệ luồng nghiệp vụ chính.
//
// 📖 2. SOURCE OF TRUTH
//   - Envoy Proxy đóng vai trò là điểm tối cao sinh và cấu hình cả hai header traceparent và X-Request-ID.
//   - Header name constants được khai báo và quản lý tập trung tại pkg/constant/http_header.go.
//
// 🚧 3. SYSTEM BOUNDARY
//   - Đóng vai trò là cổng giám sát chất lượng (Observability Gateway) tích hợp sâu với OpenTelemetry
//     và Prometheus, làm việc trực tiếp trên luồng HTTP trước khi phân phối request vào các module xử lý nghiệp vụ.
//
// 💡 4. OPERATIONAL NOTES
//   - Thứ tự thực thi: RequestID chạy trước để chuẩn bị ID định danh đưa vào logger context, sau đó
//     OTelTraceContext chạy tiếp theo để trích xuất hoặc tạo Span tracing mới.
//   - Cardinality Control: Sử dụng khuôn mẫu route từ c.FullPath() thay vì URL động chứa ID cụ thể
//     nhằm tránh phình to dữ liệu nhãn (Metric Cardinality Explosion) gây tràn RAM hệ thống Prometheus.

package middleware

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"controlplane/internal/observability"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
)

// OTelTraceContext là middleware tích hợp OpenTelemetry Tracing để giám sát phân tán.
func OTelTraceContext(obs *observability.OTel) gin.HandlerFunc {
	return func(c *gin.Context) {
		// [FAIL-OPEN CONTRACT]: Nếu module OpenTelemetry chưa cấu hình, bỏ qua an toàn và tiếp tục nghiệp vụ.
		if obs == nil {
			c.Next()
			return
		}

		// --------------------------------------------------------------------
		// 🔄 Trích xuất Context Tracing cha từ HTTP Headers (W3C traceparent).
		// --------------------------------------------------------------------
		ctx := c.Request.Context()
		ctx = obs.Extract(ctx, c.Request.Header)

		// --------------------------------------------------------------------
		// 🔄 Khởi tạo Span con mới đại diện cho thời gian thực thi tại Controlplane.
		// --------------------------------------------------------------------
		spanName := fmt.Sprintf("%s %s", c.Request.Method, requestRoute(c))
		ctx, span := obs.StartServerSpan(ctx, spanName)
		defer span.End()

		// --------------------------------------------------------------------
		// 🔄 Tiêm ngược context cập nhật vào request header và response header
		//   để các thành phần microservices phía sau kế thừa.
		// --------------------------------------------------------------------
		obs.Inject(ctx, c.Request.Header)
		if tp := strings.TrimSpace(c.Request.Header.Get(constant.HeaderTraceparent)); tp != "" {
			c.Header(constant.HeaderTraceparent, tp)
		}

		// Ghi nhận context mới vào Request Context và chuyển tiếp xử lý:
		c.Request = c.Request.WithContext(ctx)
		c.Next()

		// --------------------------------------------------------------------
		// 🔄 Bổ sung các thông số đo đạc hiệu năng (Attributes) vào Span trước khi đóng.
		// --------------------------------------------------------------------
		span.SetAttributes(
			attribute.String("http.method", c.Request.Method),
			attribute.String("http.route", requestRoute(c)),
			attribute.Int("http.status_code", c.Writer.Status()),
		)
		if len(c.Errors) > 0 {
			span.RecordError(c.Errors.Last())
		}
	}
}

// PrometheusHTTPMetrics thu thập các thông số vận hành (Telemetry Metrics) ở tầng HTTP toàn cục.
//
// ⚠️ LƯU Ý CHO DEVELOPERS:
// Middleware này hoạt động ở cấp độ toàn cục (Global). Để tránh trùng lặp số liệu (Double-counting),
// tuyệt đối không tự ý đăng ký hoặc đo đạc lại các metrics HTTP ở tầng Route-level hay Handler-level nữa.
//
// Các metrics thu thập tự động bao gồm:
//  1. [Gauge] Namespace_http_in_flight_requests: Theo dõi số lượng request xử lý đồng thời.
//  2. [Counter] Namespace_http_requests_total: Tổng số lượng HTTP request đã xử lý thành công/thất bại.
//  3. [Histogram] Namespace_http_request_duration_seconds: Latency xử lý request chi tiết theo method, route, status.
func PrometheusHTTPMetrics(obs *observability.Prometheus) gin.HandlerFunc {
	return func(c *gin.Context) {
		// [FAIL-OPEN CONTRACT]: Bỏ qua đo lường an toàn nếu Prometheus chưa khởi tạo thành công.
		if obs == nil {
			c.Next()
			return
		}

		policy := obs.GetPolicy()
		if policy == nil || !policy.Enabled {
			c.Next()
			return
		}

		start := time.Now()
		obs.IncInFlight()
		defer obs.DecInFlight()

		c.Next()

		// Ghi nhận số liệu thống kê đầy đủ vào Prometheus:
		obs.ObserveRequest(
			c.Request.Method,
			requestRoute(c),
			strconv.Itoa(c.Writer.Status()),
			time.Since(start),
		)
	}
}

// PrometheusMetricsEndpoint xuất bản endpoint (/metrics) để hệ thống Prometheus Server cào thông tin (scrape).
func PrometheusMetricsEndpoint(obs *observability.Prometheus) gin.HandlerFunc {
	return func(c *gin.Context) {
		if obs == nil {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}

		policy := obs.GetPolicy()
		if policy == nil || !policy.Enabled || !policy.ExposeMetric.Enabled {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}

		// Xác minh đường dẫn động cào dữ liệu:
		expectedPath := "/metrics"
		if policy.ExposeMetric.RoutePath != "" {
			expectedPath = policy.ExposeMetric.RoutePath
		}
		if c.Request.URL.Path != expectedPath {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}

		gin.WrapH(obs.HTTPHandler())(c)
	}
}

// requestRoute chuẩn hóa đường dẫn URL thành dạng khuôn mẫu chung để tránh phình to dữ liệu nhãn (Metric Cardinality Explosion).
func requestRoute(c *gin.Context) string {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return "/"
	}

	// 1. Ưu tiên hàng đầu: Lấy khuôn mẫu đăng ký của Gin Route (ví dụ: "/users/:id") để gộp nhóm dữ liệu
	if fullPath := strings.TrimSpace(c.FullPath()); fullPath != "" {
		return fullPath
	}

	// 2. Dự phòng (Fallback): Sử dụng đường dẫn thô nếu không khớp route nào (lỗi 404 hoặc file tĩnh)
	path := strings.TrimSpace(c.Request.URL.Path)
	if path == "" {
		return "/"
	}

	return path
}

// RequestID chịu trách nhiệm quản lý, kế thừa và đồng bộ mã giao dịch (Request ID) xuyên suốt hệ thống log.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		// --------------------------------------------------------------------
		// 🔄 Ưu tiên hàng đầu: Đọc và kế thừa X-Request-ID được tạo bởi Envoy ở biên giới.
		// --------------------------------------------------------------------
		reqID := strings.TrimSpace(c.GetHeader(constant.HeaderXRequestID))

		// --------------------------------------------------------------------
		// 🔄 Dự phòng cấp 1: Trích xuất trực tiếp Trace ID từ traceparent để đồng bộ hóa Log & Trace.
		// --------------------------------------------------------------------
		if reqID == "" {
			if tp := strings.TrimSpace(c.GetHeader(constant.HeaderTraceparent)); tp != "" {
				parts := strings.Split(tp, "-")
				if len(parts) >= 2 && len(parts[1]) == 32 {
					reqID = parts[1] // Lấy Trace ID từ định dạng W3C
				}
			}
		}

		// --------------------------------------------------------------------
		// 🔄 Dự phòng cấp 2 (Fallback): Tự sinh UUID ngẫu nhiên nếu chạy offline không qua Envoy.
		// --------------------------------------------------------------------
		if reqID == "" {
			buf := make([]byte, 16)
			if _, err := cryptorand.Read(buf); err != nil {
				reqID = fmt.Sprintf("req-%d", time.Now().UTC().UnixNano())
			} else {
				reqID = hex.EncodeToString(buf)
			}
		}

		// --------------------------------------------------------------------
		// 🔄 Nhúng Request ID vào Gin Context và phản hồi về client thông qua headers.
		// --------------------------------------------------------------------
		c.Set(logger.KeyRequestID, reqID)
		c.Header(constant.HeaderXRequestID, reqID)

		c.Next()
	}
}
