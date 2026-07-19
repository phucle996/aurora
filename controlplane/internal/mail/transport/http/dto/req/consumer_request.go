package mailReq

import (
	mailEntity "controlplane/internal/mail/domain/entity"
)

type CreateConsumerRequest struct {
	Code             string                `json:"code" binding:"required,min=3,max=63"`
	Name             string                `json:"name" binding:"required"`
	SourceType       mailEntity.SourceType `json:"source_type" binding:"required,oneof=kafka"`
	BrokerResourceID string                `json:"broker_resource_id" binding:"required"`
	Topic            string                `json:"topic" binding:"required"`
	ConsumerGroup    string                `json:"consumer_group" binding:"required"`
	Mapping          MessageMappingRequest `json:"mapping" binding:"required"`
	TemplateID       string                `json:"template_id" binding:"required"`
	TemplateVersion  uint64                `json:"template_version" binding:"required,min=1"`
	SenderProfileID  string                `json:"sender_profile_id" binding:"required"`
	SenderVersion    uint64                `json:"sender_version" binding:"required,min=1"`
	Parallelism      uint32                `json:"parallelism" binding:"required,min=1,max=256"`
}

type UpdateConsumerRequest struct {
	Name                  string                          `json:"name" binding:"required"`
	SourceType            mailEntity.SourceType           `json:"source_type" binding:"required,oneof=kafka"`
	BrokerResourceID      string                          `json:"broker_resource_id" binding:"required"`
	Topic                 string                          `json:"topic" binding:"required"`
	ConsumerGroup         string                          `json:"consumer_group" binding:"required"`
	Mapping               MessageMappingRequest           `json:"mapping" binding:"required"`
	TemplateID            string                          `json:"template_id" binding:"required"`
	TemplateVersion       uint64                          `json:"template_version" binding:"required,min=1"`
	SenderProfileID       string                          `json:"sender_profile_id" binding:"required"`
	SenderVersion         uint64                          `json:"sender_version" binding:"required,min=1"`
	DesiredState          mailEntity.ConsumerDesiredState `json:"desired_state" binding:"required,oneof=enabled paused"`
	Parallelism           uint32                          `json:"parallelism" binding:"required,min=1,max=256"`
	ExpectedConfigVersion uint64                          `json:"expected_config_version" binding:"required,min=1"`
}

type MessageMappingRequest struct {
	ExternalMessageIDJSONPath string            `json:"external_message_id_json_path"`
	RecipientJSONPath         string            `json:"recipient_json_path" binding:"required"`
	VariableJSONPaths         map[string]string `json:"variable_json_paths"`
}

type ChangeConsumerStateRequest struct {
	ExpectedConfigVersion uint64 `json:"expected_config_version" binding:"required,min=1"`
}

type DeleteConsumerRequest struct {
	ExpectedConfigVersion uint64 `json:"expected_config_version" binding:"required,min=1"`
	DrainTimeoutSeconds   uint32 `json:"drain_timeout_seconds" binding:"required,min=1,max=3600"`
	Reason                string `json:"reason"`
}
