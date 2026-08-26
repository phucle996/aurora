package mailEntity

import (
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: Tenant mutation giữ actor audit vì nhiều member có thể thay đổi cùng aggregate.
// Các flow vẫn dùng entity phẳng riêng để boundary không mang field ngoài ngữ cảnh.
type CreateTenantConsumer struct {
	ActorUserID          uuid.UUID
	TenantID             uuid.UUID
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
	CreatedBy            uuid.UUID
	UpdatedBy            uuid.UUID
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type GetTenantConsumer struct {
	ActorUserID          uuid.UUID
	TenantID             uuid.UUID
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
	CreatedBy            uuid.UUID
	UpdatedBy            uuid.UUID
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type ListTenantConsumer struct {
	ActorUserID      uuid.UUID
	TenantID         uuid.UUID
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
	CreatedBy        uuid.UUID
	UpdatedBy        uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type UpdateTenantConsumer struct {
	ActorUserID           uuid.UUID
	TenantID              uuid.UUID
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
	CreatedBy             uuid.UUID
	UpdatedBy             uuid.UUID
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type ChangeTenantConsumerState struct {
	ActorUserID           uuid.UUID
	TenantID              uuid.UUID
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

type DeleteTenantConsumer struct {
	ActorUserID           uuid.UUID
	TenantID              uuid.UUID
	ZoneID                uuid.UUID
	ID                    uuid.UUID
	WorkspaceID           uuid.UUID
	ExpectedConfigVersion uint64
	DrainTimeoutSeconds   uint32
	Reason                string
	OperationID           uuid.UUID
	UpdatedAt             time.Time
}
