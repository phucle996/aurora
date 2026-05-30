package mailEntity

import "time"

type SourceType string

const (
	Kafka       SourceType = "kafka"
	RedisStream SourceType = "redis_stream"
	RabbitMQ    SourceType = "rabbitmq"
	NATS        SourceType = "nats"
)

type ConsumerStatus string

const (
	Enabled  ConsumerStatus = "enabled"
	Paused   ConsumerStatus = "paused"
	Error    ConsumerStatus = "error"
	Draining ConsumerStatus = "draining"
)

type Consumer struct {
	ID              string
	TenantID        string
	Name            string
	SourceType      SourceType
	SourceConfigRef string
	Status          ConsumerStatus
	Parallelism     int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
