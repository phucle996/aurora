package mailEntity

type SourceType string

const (
	// [COMMENT]: SourceType chỉ dispatch suite; connect/consume/settlement không dùng chung giữa các broker.
	Kafka         SourceType = "kafka"
	RedisStream   SourceType = "redis_stream"
	RabbitMQ      SourceType = "rabbitmq"
	NATSJetStream SourceType = "nats_jetstream"
)

type ConsumerDesiredState string

const (
	ConsumerPaused   ConsumerDesiredState = "paused"
	ConsumerEnabled  ConsumerDesiredState = "enabled"
	ConsumerDeleting ConsumerDesiredState = "deleting"
	ConsumerDeleted  ConsumerDesiredState = "deleted"
)

// PersonalConsumer là entity duy nhất đi xuyên handler -> service -> Personal repository.

// TenantConsumer tách biệt hoàn toàn với Personal và luôn mang TenantID đã được ACR xác minh.
