package mailMetrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	JobsEnqueuedCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mail_jobs_enqueued_total",
			Help: "Total number of mail jobs enqueued into Redis",
		},
		[]string{"tenant_id", "status"},
	)

	ConsumerMessagesProcessedCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mail_consumer_messages_processed_total",
			Help: "Total number of messages processed by mail consumers",
		},
		[]string{"tenant_id", "consumer_id", "source_type", "status"},
	)

	// EndpointOperationsCounter tracks physical Mail Endpoint administration and health operations.
	EndpointOperationsCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mail_endpoint_operations_total",
			Help: "Total number of mail endpoint admin and test operations executed",
		},
		[]string{"operation", "zone_id", "outcome"},
	)
)
