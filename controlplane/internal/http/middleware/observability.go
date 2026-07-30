package middleware

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"controlplane/internal/observability"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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
		ctx = logger.WithCorrelation(ctx)

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
		if tp := pkgcontext.GetTraceparent(c); tp != "" {
			c.Header("traceparent", tp)
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
		if correlation, ok := logger.CorrelationFromContext(c.Request.Context()); ok {
			if correlation.Module != "" {
				span.SetAttributes(attribute.String("aurora.module", correlation.Module))
			}
			if correlation.Operation != "" {
				span.SetAttributes(attribute.String("aurora.operation", correlation.Operation))
			}
			if correlation.Observed {
				span.SetAttributes(
					attribute.String("aurora.result", correlation.Result),
					attribute.String("aurora.reason", correlation.Reason),
				)
			}
		}
		if c.Writer.Status() >= 500 {
			// HTTP status is already bounded and does not copy provider errors into traces.
			span.SetStatus(codes.Error, "http_5xx")
		}
	}
}

// OTelHTTPMetrics thu thập các thông số vận hành (Telemetry Metrics) ở tầng HTTP toàn cục.
//
// ⚠️ LƯU Ý CHO DEVELOPERS:
// Middleware này hoạt động ở cấp độ toàn cục (Global). Để tránh trùng lặp số liệu (Double-counting),
// tuyệt đối không tự ý đo đạc lại các metrics HTTP ở tầng Route-level hay Handler-level nữa.
//
// Các metrics thu thập tự động bao gồm:
//  1. [UpDownCounter] http_in_flight_requests: Theo dõi số lượng request xử lý đồng thời.
//  2. [Counter] http_requests_total: Tổng số lượng HTTP request đã xử lý thành công/thất bại.
//  3. [Histogram] http_request_duration_seconds: Latency xử lý request chi tiết theo method, route, status.
func OTelHTTPMetrics(obs *observability.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		// [FAIL-OPEN CONTRACT]: Bỏ qua đo lường an toàn nếu Metrics chưa khởi tạo thành công.
		if !obs.Enabled() {
			c.Next()
			return
		}

		start := time.Now()
		obs.AddHTTPInFlight(c.Request.Context(), c.Request.Method, 1)
		defer obs.AddHTTPInFlight(c.Request.Context(), c.Request.Method, -1)

		c.Next()

		// Ghi nhận số liệu thống kê đầy đủ vào OTel Metrics:
		obs.ObserveHTTPRequest(
			c.Request.Context(),
			c.Request.Method,
			requestRoute(c),
			strconv.Itoa(c.Writer.Status()),
			time.Since(start),
		)
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

	// Raw unmatched paths are attacker-controlled and would create an unbounded
	// metric label space, so every unmatched request uses one sentinel.
	return "__unmatched__"
}

// RequestID chịu trách nhiệm quản lý, kế thừa và đồng bộ mã giao dịch (Request ID) xuyên suốt hệ thống log.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		// The root request must own the carrier before handlers derive timeout
		// contexts; otherwise access logging cannot observe the service outcome.
		c.Request = c.Request.WithContext(logger.WithCorrelation(c.Request.Context()))
		// --------------------------------------------------------------------
		// 🔄 Ưu tiên hàng đầu: Đọc và kế thừa X-Request-ID được tạo bởi Envoy ở biên giới.
		// --------------------------------------------------------------------
		reqID := pkgcontext.GetRequestID(c)

		// --------------------------------------------------------------------
		// 🔄 Dự phòng cấp 1: Trích xuất trực tiếp Trace ID từ traceparent để đồng bộ hóa Log & Trace.
		// --------------------------------------------------------------------
		if reqID == "" {
			if tp := pkgcontext.GetTraceparent(c); tp != "" {
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
		c.Header("X-Request-ID", reqID)

		// [COMMENT]: Trích xuất IP và UserAgent ở đầu luồng HTTP để tiêm vào Context, tách biệt hạ tầng mạng khỏi logic nghiệp vụ.
		ip := strings.TrimSpace(c.ClientIP())
		ua := strings.TrimSpace(c.Request.UserAgent())
		ctx := context.WithValue(c.Request.Context(), RemoteIPKey, ip)
		ctx = context.WithValue(ctx, UserAgentKey, ua)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}
