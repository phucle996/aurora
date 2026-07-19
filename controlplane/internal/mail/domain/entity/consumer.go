package mailEntity

type SourceType string

const (
	// [COMMENT]: Kafka là source được runtime phase đầu hỗ trợ; các enum còn lại dành cho contract mở rộng.
	Kafka       SourceType = "kafka"
	RedisStream SourceType = "redis_stream"
	RabbitMQ    SourceType = "rabbitmq"
	NATS        SourceType = "nats"
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

type MessageMapping struct {
	ExternalMessageIDJSONPath string            `json:"external_message_id_json_path,omitempty"`
	RecipientJSONPath         string            `json:"recipient_json_path"`
	VariableJSONPaths         map[string]string `json:"variable_json_paths"`
}
