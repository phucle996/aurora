// ============================================================================
// 📂 MODULE: controlplane/internal/mail/metrics/metrics.go
//            Đo Lường Chỉ Số Vận Hành Dịch Vụ Mail (OTel Metrics)
// ============================================================================

package mailMetrics

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	initOnce sync.Once

	jobsEnqueuedCounter              metric.Int64Counter
	consumerMessagesProcessedCounter metric.Int64Counter
	endpointOperationsCounter        metric.Int64Counter
)

// ensureInit khởi tạo OTel instruments cho Mail metrics.
func ensureInit() {
	initOnce.Do(func() {
		meter := otel.Meter("aurora-controlplane.mail")

		jobsEnqueuedCounter, _ = meter.Int64Counter(
			"mail_jobs_enqueued_total",
			metric.WithDescription("Total number of mail jobs enqueued into Redis"),
		)
		consumerMessagesProcessedCounter, _ = meter.Int64Counter(
			"mail_consumer_messages_processed_total",
			metric.WithDescription("Total number of messages processed by mail consumers"),
		)
		endpointOperationsCounter, _ = meter.Int64Counter(
			"mail_endpoint_operations_total",
			metric.WithDescription("Total number of mail endpoint admin and test operations executed"),
		)
	})
}

// IncJobsEnqueued tăng số lượng mail jobs enqueued.
func IncJobsEnqueued(tenantID, status string) {
	ensureInit()
	if jobsEnqueuedCounter != nil {
		jobsEnqueuedCounter.Add(context.Background(), 1, metric.WithAttributes(
			attribute.String("tenant_id", tenantID),
			attribute.String("status", status),
		))
	}
}

// IncConsumerMessagesProcessed tăng số lượng messages được xử lý bởi mail consumers.
func IncConsumerMessagesProcessed(tenantID, consumerID, sourceType, status string) {
	ensureInit()
	if consumerMessagesProcessedCounter != nil {
		consumerMessagesProcessedCounter.Add(context.Background(), 1, metric.WithAttributes(
			attribute.String("tenant_id", tenantID),
			attribute.String("consumer_id", consumerID),
			attribute.String("source_type", sourceType),
			attribute.String("status", status),
		))
	}
}

// IncEndpointOperations ghi nhận hoạt động nghiệp vụ trên Mail Endpoint.
func IncEndpointOperations(operation, zoneID, outcome string) {
	ensureInit()
	if endpointOperationsCounter != nil {
		endpointOperationsCounter.Add(context.Background(), 1, metric.WithAttributes(
			attribute.String("operation", operation),
			attribute.String("zone_id", zoneID),
			attribute.String("outcome", outcome),
		))
	}
}
