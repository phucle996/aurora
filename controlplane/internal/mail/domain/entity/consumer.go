package mailEntity

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

type ConsumerDesiredState string

const (
	ConsumerPaused   ConsumerDesiredState = "paused"
	ConsumerEnabled  ConsumerDesiredState = "enabled"
	ConsumerDraining ConsumerDesiredState = "draining"
	ConsumerDrained  ConsumerDesiredState = "drained"
	ConsumerDeleting ConsumerDesiredState = "deleting"
)
