package mailReq

import (
	mailEntity "controlplane/internal/mail/domain/entity"
)

type CreateConsumerRequest struct {
	Code             string                `json:"code" binding:"required,min=3,max=63"`
	Name             string                `json:"name" binding:"required"`
	SourceType       mailEntity.SourceType `json:"source_type" binding:"required,oneof=kafka redis_stream nats_jetstream rabbitmq"`
	BrokerResourceID string                `json:"broker_resource_id" binding:"required"`
	// [COMMENT]: Base64 của encrypted broker envelope; client không được gửi plaintext credentials.
	SourceConfigEnvelope string `json:"source_config_envelope"`
	// [COMMENT]: Hai field có tên lịch sử nhưng handler diễn giải theo suite: topic/group, stream/group, stream/durable hoặc queue/tag-prefix.
	Topic           string `json:"topic" binding:"required"`
	ConsumerGroup   string `json:"consumer_group" binding:"required"`
	TemplateID      string `json:"template_id" binding:"required"`
	TemplateVersion uint64 `json:"template_version" binding:"required,min=1"`
	SenderProfileID string `json:"sender_profile_id" binding:"required"`
	SenderVersion   uint64 `json:"sender_version" binding:"required,min=1"`
	Parallelism     uint32 `json:"parallelism" binding:"required,min=1,max=256"`
}

type UpdateConsumerRequest struct {
	Name             string                `json:"name" binding:"required"`
	SourceType       mailEntity.SourceType `json:"source_type" binding:"required,oneof=kafka redis_stream nats_jetstream rabbitmq"`
	BrokerResourceID string                `json:"broker_resource_id" binding:"required"`
	// [COMMENT]: Base64 của encrypted broker envelope; client không được gửi plaintext credentials.
	SourceConfigEnvelope  string                          `json:"source_config_envelope"`
	Topic                 string                          `json:"topic" binding:"required"`
	ConsumerGroup         string                          `json:"consumer_group" binding:"required"`
	TemplateID            string                          `json:"template_id" binding:"required"`
	TemplateVersion       uint64                          `json:"template_version" binding:"required,min=1"`
	SenderProfileID       string                          `json:"sender_profile_id" binding:"required"`
	SenderVersion         uint64                          `json:"sender_version" binding:"required,min=1"`
	DesiredState          mailEntity.ConsumerDesiredState `json:"desired_state" binding:"required,oneof=enabled paused"`
	Parallelism           uint32                          `json:"parallelism" binding:"required,min=1,max=256"`
	ExpectedConfigVersion uint64                          `json:"expected_config_version" binding:"required,min=1"`
}

type ChangeConsumerStateRequest struct {
	ExpectedConfigVersion uint64 `json:"expected_config_version" binding:"required,min=1"`
}

type DeleteConsumerRequest struct {
	ExpectedConfigVersion string `json:"expected_config_version"`
	Reason                string `json:"reason"`
}
