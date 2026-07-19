package mailEntity

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type PersonalConsumer struct {
	ActorUserID      uuid.UUID
	ZoneID           uuid.UUID
	ID               uuid.UUID
	WorkspaceID      uuid.UUID
	Code             string
	Name             string
	SourceType       SourceType
	BrokerResourceID uuid.UUID
	// [COMMENT]: Internal Vault locator do service derive từ trusted scope; HTTP request/response không nhận hoặc echo field này.
	SourceConfigRef       string
	Topic                 string
	ConsumerGroup         string
	MappingJSON           json.RawMessage
	TemplateID            string
	TemplateVersion       uint64
	SenderProfileID       string
	SenderVersion         uint64
	DesiredState          ConsumerDesiredState
	Parallelism           uint32
	ConfigVersion         uint64
	ConfigSHA256          []byte
	DeletedAt             *time.Time
	CreatedBy             *uuid.UUID
	UpdatedBy             *uuid.UUID
	CreatedAt             time.Time
	UpdatedAt             time.Time
	ExpectedConfigVersion uint64
	DrainTimeoutSeconds   uint32
	Reason                string
	AfterID               *uuid.UUID
	Limit                 uint32
	Mapping               MessageMapping
}
