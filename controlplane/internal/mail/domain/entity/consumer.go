package mailEntity

import "time"

type SourceType string

const (
	// [COMMENT]: SourceType chỉ dispatch suite; connect/consume/settlement không dùng chung giữa các broker.
	Kafka         SourceType = "kafka"
	RedisStream   SourceType = "redis_stream"
	RabbitMQ      SourceType = "rabbitmq"
	NATSJetStream SourceType = "nats_jetstream"
)

type ConsumerRuntimeState string

const (
	ConsumerRuntimeStopped  ConsumerRuntimeState = "stopped"
	ConsumerRuntimeStarting ConsumerRuntimeState = "starting"
	ConsumerRuntimeRunning  ConsumerRuntimeState = "running"
	ConsumerRuntimePaused   ConsumerRuntimeState = "paused"
	ConsumerRuntimeDraining ConsumerRuntimeState = "draining"
	ConsumerRuntimeError    ConsumerRuntimeState = "error"
	ConsumerRuntimeDegraded ConsumerRuntimeState = "degraded"
)

// ConsumerRuntimeSummary là current operational read model dùng chung cho Personal/Tenant detail.
// Nó không chứa recipient, rendered payload hay lịch sử từng email.
type ConsumerRuntimeSummary struct {
	State           ConsumerRuntimeState
	ConfigVersion   uint64
	ActiveInstances uint32
	ConsumerLag     uint64
	ErrorCode       string
	ErrorMessage    string
	ReportedAt      time.Time
	NextExpiryAt    time.Time
}

type ConsumerDesiredState string

const (
	ConsumerPaused   ConsumerDesiredState = "paused"
	ConsumerEnabled  ConsumerDesiredState = "enabled"
	ConsumerDeleting ConsumerDesiredState = "deleting"
	ConsumerDeleted  ConsumerDesiredState = "deleted"
)

// PersonalConsumer là entity duy nhất đi xuyên handler -> service -> Personal repository.

// TenantConsumer tách biệt hoàn toàn với Personal và luôn mang TenantID đã được ACR xác minh.
