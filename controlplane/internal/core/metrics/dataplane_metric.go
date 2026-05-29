// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/metrics/dataplane_metric.go
//            Đặc Tả Chỉ Số Đo Lường Heartbeat Của Dataplane Registry (Prometheus Telemetry)
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & SỰ PHÙ HỢP VẬN HÀNH (CONTRACT & SRE OBSERVABILITY):
//   - Định nghĩa các chỉ số đo lường hiệu năng (Telemetry Metrics) để giám sát trạng thái nhịp tim
//     của Dataplane gửi lên Controlplane theo thời gian thực.
//   - Đảm bảo SRE có cái nhìn trực quan và dễ dàng thiết lập cảnh báo (Alerting) trên Grafana:
//
//     1) HIGH-FIDELITY HEARTBEAT MONITORING:
//        * Metric `aurora_controlplane_core_dataplane_heartbeat_total` đếm tổng số lượng heartbeat
//          nhận được phân tách chi tiết theo luồng (`path` = "pubsub" | "grpc") và kết quả (`result` = "success" | "failure").
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Đăng ký trực tiếp vào Prometheus Registry dùng chung của hệ thống.
//
// 🔒 RANH GIỚI BẢO MẬT & KIẾN TRÚC (CRITICAL ARCHITECTURAL BOUNDARY):
//   - Chỉ chứa các hàm trợ giúp tăng/giảm metric an toàn, tuyệt đối không chứa business logic hay validation.
//   - An toàn tuyệt đối: Không ném Exception/Panic làm gián đoạn luồng xử lý chính nếu Prometheus lỗi.
//
// ======================================================================================================

package coreMetric

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	dataplaneHeartbeatTotal *prometheus.CounterVec
)

// InitDataplaneMetrics khởi tạo các counter cho Dataplane.
func InitDataplaneMetrics(namespace string) {
	dataplaneHeartbeatTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "core",
		Name:      "dataplane_heartbeat_total",
		Help:      "Total count of dataplane heartbeat messages received.",
	}, []string{"path", "result"})
}

// ObserveHeartbeat ghi nhận một nhịp tim nhận được từ Dataplane.
func ObserveHeartbeat(path string, result string) {
	if dataplaneHeartbeatTotal != nil {
		dataplaneHeartbeatTotal.WithLabelValues(path, result).Inc()
	}
}
