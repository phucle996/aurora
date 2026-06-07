// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/metrics/zone_metric.go
//            Đặc Tả Chỉ Số Đo Lường Nghiệp Vụ Zone (Prometheus Telemetry)
// ======================================================================================================

package coreMetric

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	zoneOperationsTotal *prometheus.CounterVec
)

// InitZoneMetrics khởi tạo các metrics cho Zone.
func InitZoneMetrics(namespace string) {
	zoneOperationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "core",
		Name:      "zone_operations_total",
		Help:      "Total count of zone operations by operation type and outcome.",
	}, []string{"operation", "outcome"})
}

// ObserveZoneOperation ghi nhận một hành động thực thi với Zone kèm outcome.
func ObserveZoneOperation(operation string, outcome string) {
	if zoneOperationsTotal != nil {
		zoneOperationsTotal.WithLabelValues(operation, outcome).Inc()
	}
}
