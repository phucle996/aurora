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

/*
===========================================================================================
🔍 OBSERVABILITY BOUNDARY & CONTRACT SPECIFICATION
===========================================================================================

1. CORE MISSION:
   - Chịu trách nhiệm đồng bộ, kế thừa và phân phối định danh giao dịch (Transaction Tracing)
     xuyên suốt từ Client -> Envoy Edge Proxy -> Controlplane -> Dataplane và các dịch vụ bổ trợ.
   - Hợp nhất 2 thế giới giám sát độc lập: Logs (dạng văn bản phẳng) và Traces (dự án đồ thị APM).

2. THE 2 POWER HEADERS (traceparent & X-Request-ID):
   a. "traceparent" (Tiêu chuẩn W3C):
      - Mục đích: Distributed Tracing bằng OpenTelemetry (APM - Grafana Tempo / Jaeger).
      - Cơ chế: Mang cấu trúc cây: [version]-[trace_id]-[parent_span_id]-[flags].
      - Cách hoạt động: Khi đi qua mỗi chặng (Hop), OTel SDK sẽ trích xuất context này,
        tạo một Span con mới (đại diện cho tác vụ xử lý nội tại) và liên kết ngược lại Span cha.
        Nhờ đó, Grafana Tempo có thể vẽ biểu đồ thời gian thực thi (Latency Graph) chính xác từng mili-giây.
      - **VISIBILITY CONTRACT [KHÔNG TRẢ VỀ CLIENT]**: Header này là tài sản nội bộ. Envoy Edge Proxy
        được cấu hình `response_headers_to_remove` để tự động triệt tiêu nó trước khi trả phản hồi
        về Internet, ngăn chặn rò rỉ thông tin hạ tầng (Infrastructure Leakage).
   b. "X-Request-ID" (Custom Header):
      - Mục đích: Log Aggregation (Grafana Loki / Splunk).
      - Cơ chế: Một chuỗi định danh phẳng ngẫu nhiên (UUID hoặc Hex) truyền từ đầu đến cuối request.
      - Cách hoạt động: Được in ra trực tiếp trong mọi dòng log văn bản thô. Giúp kỹ sư dùng để lọc
        (filter) toàn bộ các dòng log liên đới của duy nhất một giao dịch trên giao diện tìm kiếm Loki.
      - **VISIBILITY CONTRACT [CÓ TRẢ VỀ CLIENT]**: Được gửi về lại trình duyệt của Client. Khi có lỗi
        hệ thống, Client sẽ báo mã này cho kỹ sư để tra cứu log thô và liên kết sang trace tương ứng.

3. SOURCE OF TRUTH (SoT):
   - ENVOY PROXY đóng vai trò là "Biên giới tối cao" sinh ra và cấu hình cả 2 headers này:
     - Envoy tạo "X-Request-ID" thông qua cấu hình `generate_request_id: true`.
     - Envoy tạo "traceparent" khi tiếp nhận kết nối HTTP/HTTPS từ Client bên ngoài.
   - CONTROLPLANE cam kết INHERITANCE FIRST các giá trị này để đảm bảo
     tính liên tục của luồng trace. Chỉ khi chạy test cục bộ hoặc không có Envoy, Controlplane
     mới tự sinh ngẫu nhiên làm Fallback Mechanism.

4. MIDDLEWARE PIPELINE EXECUTION ORDER:
   - RequestID() chạy đầu tiên -> Đọc "X-Request-ID" hoặc trích xuất "Trace ID" từ "traceparent" -> c.Set(logger.KeyRequestID)
   - OTelTraceContext() chạy tiếp theo -> obs.Extract() nhận context cha -> Bắt đầu Server Span con mới.

5. SYSTEM DESIGN - FAIL-OPEN CONTRACT:
   - Triết lý: TELEMETRY IS AUXILIARY - BUSINESS LOGIC IS PRIMARY.
   - Nếu hạ tầng đo lường (Tempo, Prometheus, Jaeger, OTel Collector) bị sập hoặc lỗi khởi tạo,
     hệ thống Controlplane cam kết thực hiện FAIL-OPEN.
   - Tuyệt đối không được chặn request (Abort) hay báo lỗi 500 khi thiếu thông số giám sát,
     thay vào đó phải gọi `c.Next()` để phục vụ người dùng cuối, đồng thời áp dụng cơ chế tự sinh
     Request ID nội bộ an toàn (Fallback Mechanism) nhằm không bao giờ bỏ sót log audit.
===========================================================================================
*/

// HeaderTraceparent và HeaderXRequestID hiện đã được quản lý tập trung tại package constant
// (pkg/constant/http_header.go) làm Single Source of Truth (SoT).

// OTelTraceContext là middleware tích hợp OpenTelemetry Tracing.
// Nhiệm vụ:
//  1. Trích xuất (Extract) context tracing cha được gửi tới từ Envoy Proxy qua HTTP Headers.
//  2. Khởi tạo một Server Span con mới biểu thị cho thời gian thực thi request tại Controlplane.
//  3. Tiêm ngược (Inject) context đã cập nhật vào request tiếp theo và đặt vào response headers.
func OTelTraceContext(obs *observability.OTel) gin.HandlerFunc {
	return func(c *gin.Context) {
		// [FAIL-OPEN CONTRACT]: Nếu module OpenTelemetry (obs) chưa được cấu hình hoặc bị lỗi,
		// bỏ qua tracing một cách an toàn và tiếp tục xử lý nghiệp vụ chính. Không chặn đứng request.
		if obs == nil {
			c.Next()
			return
		}

		// Bước 1: Trích xuất Context Tracing cha từ HTTP Headers (W3C traceparent).
		// Nếu Envoy đã đính kèm traceparent, OTel SDK sẽ khôi phục liên kết cha-con tại đây.
		// Nếu chưa có traceparent, thì sẽ tạo mới một traceparent
		ctx := c.Request.Context()
		ctx = obs.Extract(ctx, c.Request.Header)

		// Bước 2: Tạo Span con mới đại diện cho tiến trình xử lý hiện tại của API Controlplane.
		spanName := fmt.Sprintf("%s %s", c.Request.Method, requestRoute(c))
		ctx, span := obs.StartServerSpan(ctx, spanName)
		defer span.End()

		// Bước 3: Đồng bộ ngược lại context vào request header và response header
		// để các microservice tiếp theo (như Dataplane) có thể tiếp tục kế thừa.
		obs.Inject(ctx, c.Request.Header)
		if tp := strings.TrimSpace(c.Request.Header.Get(constant.HeaderTraceparent)); tp != "" {
			c.Header(constant.HeaderTraceparent, tp)
		}

		// Bước 4: Lưu context mới (mang Span hiện tại) vào Request Context và chuyển tiếp xử lý.
		c.Request = c.Request.WithContext(ctx)
		c.Next()

		// Bước 5: Bổ sung các thông số đo đạc hiệu năng (Metadata Attributes) vào Span trước khi đóng.
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

// PrometheusHTTPMetrics là middleware thu thập các thông số vận hành (Telemetry Metrics) ở tầng HTTP toàn cục.
//
// ⚠️ LƯU Ý QUAN TRỌNG CHO DEVELOPERS KHI THIẾT KẾ ROUTE / HANDLER MỚI:
// Middleware này hoạt động ở cấp độ GLOBAL (Toàn cục) cho toàn bộ các request đi qua Gin Engine.
// Để TRÁNH TRÙNG LẶP SỐ LIỆU (Double-counting), TUYỆT ĐỐI KHÔNG tự động đăng ký hoặc đo đạc lại các
// metrics HTTP sau đây ở tầng Route-level hay Handler-level nữa:
//
// Các metrics được quản lý tập trung và tự động thu thập tại đây bao gồm:
//
//  1. [Gauge] "aurora_controlplane_http_in_flight_requests" (Tên chính xác có dạng: <namespace>_http_in_flight_requests):
//     - Ý nghĩa: Theo dõi số lượng request đang xử lý đồng thời (In-Flight Requests).
//     - Hoạt động: Tăng khi nhận request (IncInFlight) và tự động giảm khi kết thúc (DecInFlight qua defer).
//
//  2. [Counter] "aurora_controlplane_http_requests_total" (Tên chính xác: <namespace>_http_requests_total):
//     - Ý nghĩa: Tổng số lượng HTTP request đã được xử lý xong bởi hệ thống.
//     - Nhãn (Labels) thu thập:
//     * "method": Phương thức HTTP (ví dụ: GET, POST, PUT, DELETE...).
//     * "route": API path đã được chuẩn hóa qua requestRoute() để tránh phình to cardinalities (ví dụ: /admin/auth/login).
//     * "status": HTTP Status Code trả về dưới dạng chuỗi (ví dụ: "200", "400", "401", "500"...).
//
//  3. [Histogram] "aurora_controlplane_http_request_duration_seconds" (Tên chính xác: <namespace>_http_request_duration_seconds):
//     - Ý nghĩa: Thời gian latency (độ trễ) xử lý toàn bộ request của API.
//     - Nhãn (Labels) thu thập: Tương tự như Counter trên ("method", "route", "status").
func PrometheusHTTPMetrics(obs *observability.Prometheus) gin.HandlerFunc {
	return func(c *gin.Context) {
		// [FAIL-OPEN CONTRACT]: Nếu hệ thống thu thập metrics Prometheus chưa sẵn sàng,
		// bỏ qua việc đo lường một cách an toàn và tiếp tục xử lý nghiệp vụ.
		if obs == nil {
			c.Next()
			return
		}

		start := time.Now()
		obs.IncInFlight()
		defer obs.DecInFlight()

		c.Next()

		// Ghi nhận số liệu toàn cục tự động vào Prometheus: Method, Path, Status Code và Latency (Duration).
		obs.ObserveRequest(
			c.Request.Method,
			requestRoute(c),
			strconv.Itoa(c.Writer.Status()),
			time.Since(start),
		)
	}
}

// PrometheusMetricsEndpoint xuất bản endpoint (/metrics) để Prometheus Server vào cào dữ liệu (scrape).
func PrometheusMetricsEndpoint(obs *observability.Prometheus) gin.HandlerFunc {
	if obs == nil {
		return func(c *gin.Context) {
			c.AbortWithStatus(http.StatusServiceUnavailable)
		}
	}
	return gin.WrapH(obs.HTTPHandler())
}

// requestRoute chuẩn hóa đường dẫn URL của HTTP Request thành một định dạng khuôn mẫu (Route Template) duy nhất.
//
// 📌 GIẢI THÍCH LÝ DO (TRÁNH METRIC CARDINALITY EXPLOSION):
// Prometheus là cơ sở dữ liệu chuỗi thời gian (TSDB). Mỗi sự kết hợp giữa [Metric Name] và [Labels/nhãn]
// sẽ tạo ra một dòng dữ liệu (Time Series) độc lập lưu trong bộ nhớ.
//
// Nếu chúng ta sử dụng đường dẫn URL thực tế chứa các ID động (ví dụ: c.Request.URL.Path = "/users/1", "/users/2"...)
// làm nhãn "route" cho Prometheus, khi hệ thống có 1.000.000 users, Prometheus sẽ tạo ra 1.000.000 Time Series.
// Hiện tượng này gọi là "Phình to dữ liệu nhãn" (Metric Cardinality Explosion), gây tràn RAM và sập Prometheus Server.
//
// 💡 GIẢI PHÁP XỬ LÝ:
// Hàm này giải quyết triệt để bằng hai bước kiểm tra:
//  1. Ưu tiên số một: Gọi c.FullPath() của Gin. Hàm này trả về khuôn mẫu đăng ký của Route (ví dụ: "/users/:id")
//     thay vì đường dẫn thực tế chứa ID động ("/users/12345"). Nhờ đó gộp toàn bộ 1 triệu request vào đúng 1 nhãn duy nhất ("/users/:id").
//  2. Dự phòng cuối cùng (Fallback): Nếu không tìm thấy Route khớp (ví dụ: lỗi 404, request scan bậy hoặc truy cập file tĩnh),
//     hàm sẽ sử dụng c.Request.URL.Path làm giá trị dự phòng.
func requestRoute(c *gin.Context) string {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return "/"
	}

	if fullPath := strings.TrimSpace(c.FullPath()); fullPath != "" {
		return fullPath
	}

	path := strings.TrimSpace(c.Request.URL.Path)
	if path == "" {
		return "/"
	}

	return path
}

// RequestID là middleware liên kết mã định danh giao dịch (Request ID) vào luồng thực thi.
// Nhiệm vụ:
//  1. Kiểm tra sự tồn tại của header "X-Request-ID" (đã được tạo bởi Envoy ở tầng biên).
//  2. Nếu không tìm thấy, cố gắng phân tích mã "Trace ID" từ W3C "traceparent" để đồng bộ hóa
//     tuyệt đối giữa hệ thống Log (Loki) và hệ thống Trace (Tempo).
//  3. Nếu không tìm thấy bất kỳ định danh nào, tự tạo mới một UUID ngẫu nhiên (Fallback inline).
//  4. Đưa định danh này vào gin.Context thông qua key "logger.KeyRequestID" phục vụ cho logger.
//  5. Thiết lập phản hồi header "X-Request-ID" về lại phía Client.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Bước 1: Ưu tiên số một - Kế thừa "X-Request-ID" từ Envoy gửi lên.
		reqID := strings.TrimSpace(c.GetHeader(constant.HeaderXRequestID))

		// Bước 2: Ưu tiên số hai - Trích xuất trực tiếp Trace ID từ traceparent để đồng bộ Log & Trace.
		if reqID == "" {
			if tp := strings.TrimSpace(c.GetHeader(constant.HeaderTraceparent)); tp != "" {
				// Định dạng W3C traceparent: version-trace_id-parent_span_id-trace_flags
				// Ví dụ: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01
				parts := strings.Split(tp, "-")
				if len(parts) >= 2 && len(parts[1]) == 32 {
					reqID = parts[1] // Trích xuất Trace ID
				}
			}
		}

		// Bước 3: Dự phòng cuối cùng - Nếu hoàn toàn không có gì (chạy test local không qua Envoy), tự tạo mới (inline).
		if reqID == "" {
			buf := make([]byte, 16)
			if _, err := cryptorand.Read(buf); err != nil {
				reqID = fmt.Sprintf("req-%d", time.Now().UTC().UnixNano())
			} else {
				reqID = hex.EncodeToString(buf)
			}
		}

		// Bước 4: Nhúng định danh vào Gin Context. Giúp tất cả các hàm gọi logger.Error() hay logger.Info()
		// tự động nhận diện và đính kèm Request ID này vào dòng log JSON.
		c.Set(logger.KeyRequestID, reqID)

		// Bước 5: Phản hồi ngược header này cho Client để tiện cho việc phối hợp debug khi có lỗi.
		c.Header(constant.HeaderXRequestID, reqID)

		c.Next()
	}
}
