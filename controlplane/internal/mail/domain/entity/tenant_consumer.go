package mailEntity

import (
	"time"

	"github.com/google/uuid"
)

type TenantConsumer struct {
	ActorUserID      uuid.UUID
	TenantID         uuid.UUID
	ZoneID           uuid.UUID
	ID               uuid.UUID
	WorkspaceID      uuid.UUID
	Code             string
	Name             string
	SourceType       SourceType
	BrokerResourceID uuid.UUID
	// [COMMENT]: Encrypted broker configuration envelope; JO/Redis chỉ chuyển tiếp bytes.
	SourceConfigEnvelope  []byte
	Topic                 string
	ConsumerGroup         string
	TemplateID            string
	TemplateVersion       uint64
	SenderProfileID       string
	SenderVersion         uint64
	DesiredState          ConsumerDesiredState
	Parallelism           uint32
	ConfigVersion         uint64
	ConfigSHA256          []byte
	CreatedBy             *uuid.UUID
	UpdatedBy             *uuid.UUID
	CreatedAt             time.Time
	UpdatedAt             time.Time
	ExpectedConfigVersion uint64
	DrainTimeoutSeconds   uint32
	Reason                string
	AfterID               *uuid.UUID
	Limit                 uint32
	Runtime               *ConsumerRuntimeSummary
}
