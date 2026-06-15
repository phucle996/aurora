// ============================================================================
// 📂 MODULE: controlplane/internal/core/metrics/zone_metric.go
//            Đo Lường Nghiệp Vụ Zone (OTel Metrics)
// ============================================================================

package coreMetric

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	// zoneOperationsTotal đếm tổng số hành động thực thi với Zone.
	zoneOperationsTotal metric.Int64Counter
)

// initZoneMetrics được gọi từ ensureInit() trong module_register.go.
func initZoneMetrics(meter metric.Meter) {
	zoneOperationsTotal, _ = meter.Int64Counter(
		"aurora_controlplane_core_zone_operations_total",
		metric.WithDescription("Total count of zone operations by operation type and outcome."),
	)
}

// ObserveZoneOperation ghi nhận một hành động thực thi với Zone kèm outcome.
func ObserveZoneOperation(operation string, outcome string) {
	ensureInit()
	if zoneOperationsTotal != nil {
		zoneOperationsTotal.Add(context.Background(), 1,
			metric.WithAttributes(
				attribute.String("operation", operation),
				attribute.String("outcome", outcome),
			),
		)
	}
}
