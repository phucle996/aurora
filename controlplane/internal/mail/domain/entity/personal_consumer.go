package mailEntity

import (
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: Mỗi Personal flow có entity phẳng riêng; actor chỉ phục vụ authorization/outbox,
// không trở thành created_by hoặc updated_by của business row cá nhân.
type CreatePersonalConsumer struct {
	ActorUserID          uuid.UUID
	ZoneID               uuid.UUID
	ID                   uuid.UUID
	WorkspaceID          uuid.UUID
	Code                 string
	Name                 string
	SourceType           SourceType
	BrokerResourceID     uuid.UUID
	SourceConfigEnvelope []byte
	Topic                string
	ConsumerGroup        string
	TemplateID           string
	TemplateVersion      uint64
	SenderProfileID      string
	SenderVersion        uint64
	DesiredState         ConsumerDesiredState
	Parallelism          uint32
	ConfigVersion        uint64
	NextConfigVersion    uint64
	ConfigSHA256         []byte
	OperationID          uuid.UUID
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type GetPersonalConsumer struct {
	ActorUserID          uuid.UUID
	ZoneID               uuid.UUID
	ID                   uuid.UUID
	WorkspaceID          uuid.UUID
	Code                 string
	Name                 string
	SourceType           SourceType
	BrokerResourceID     uuid.UUID
	SourceConfigEnvelope []byte
	Topic                string
	ConsumerGroup        string
	TemplateID           string
	TemplateVersion      uint64
	SenderProfileID      string
	SenderVersion        uint64
	DesiredState         ConsumerDesiredState
	Parallelism          uint32
	ConfigVersion        uint64
	NextConfigVersion    uint64
	ConfigSHA256         []byte
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type ListPersonalConsumer struct {
	ActorUserID      uuid.UUID
	ZoneID           uuid.UUID
	WorkspaceID      uuid.UUID
	AfterID          *uuid.UUID
	Limit            uint32
	ID               uuid.UUID
	Code             string
	Name             string
	SourceType       SourceType
	BrokerResourceID uuid.UUID
	SourceConfigured bool
	Topic            string
	ConsumerGroup    string
	TemplateID       string
	TemplateVersion  uint64
	SenderProfileID  string
	SenderVersion    uint64
	DesiredState     ConsumerDesiredState
	Parallelism      uint32
	ConfigVersion    uint64
	ConfigSHA256     []byte
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type UpdatePersonalConsumer struct {
	ActorUserID           uuid.UUID
	ZoneID                uuid.UUID
	ID                    uuid.UUID
	WorkspaceID           uuid.UUID
	Code                  string
	Name                  string
	SourceType            SourceType
	BrokerResourceID      uuid.UUID
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
	NextConfigVersion     uint64
	ExpectedConfigVersion uint64
	ConfigSHA256          []byte
	OperationID           uuid.UUID
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type ChangePersonalConsumerState struct {
	ActorUserID           uuid.UUID
	ZoneID                uuid.UUID
	ID                    uuid.UUID
	WorkspaceID           uuid.UUID
	ExpectedConfigVersion uint64
	DesiredState          ConsumerDesiredState
	Code                  string
	Name                  string
	SourceType            SourceType
	BrokerResourceID      uuid.UUID
	SourceConfigEnvelope  []byte
	Topic                 string
	ConsumerGroup         string
	TemplateID            string
	TemplateVersion       uint64
	SenderProfileID       string
	SenderVersion         uint64
	Parallelism           uint32
	ConfigSHA256          []byte
	CreatedAt             time.Time
	OperationID           uuid.UUID
	ConfigVersion         uint64
	UpdatedAt             time.Time
}

type DeletePersonalConsumer struct {
	ActorUserID           uuid.UUID
	ZoneID                uuid.UUID
	ID                    uuid.UUID
	WorkspaceID           uuid.UUID
	ExpectedConfigVersion uint64
	Reason                string
	OperationID           uuid.UUID
	UpdatedAt             time.Time
}

type PersonalConsumerDrainCommand struct {
	ActorUserID uuid.UUID
	WorkspaceID uuid.UUID
	ZoneID      uuid.UUID

	ConsumerID            uuid.UUID
	ExpectedConfigVersion uint64
	TimeoutSeconds        uint32
}
type PersonalConsumerDrainTarget struct {
	ConfigVersion uint64
	Parallelism   uint32
	State         ConsumerDesiredState
}
