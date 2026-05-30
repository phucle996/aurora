package mailReq

import (
	mailEntity "controlplane/internal/mail/domain/entity"
)

type CreateConsumerRequest struct {
	Name            string                `json:"name" binding:"required"`
	SourceType      mailEntity.SourceType `json:"source_type" binding:"required,oneof=kafka redis_stream rabbitmq nats"`
	SourceConfigRef string                `json:"source_config_ref" binding:"required"`
	Parallelism     int                   `json:"parallelism" binding:"required,min=1"`
}

type UpdateConsumerRequest struct {
	Name        string `json:"name" binding:"required"`
	Parallelism int    `json:"parallelism" binding:"required,min=1"`
}

type UpdateConsumerStatusRequest struct {
	Status mailEntity.ConsumerStatus `json:"status" binding:"required,oneof=enabled paused error draining"`
}
