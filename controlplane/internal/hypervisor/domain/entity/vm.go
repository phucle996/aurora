package hypervisorEntity

import (
	"time"

	"github.com/google/uuid"
)

type VMStatus string

const (
	VMStatusProvisioning VMStatus = "PROVISIONING"
	VMStatusReady        VMStatus = "READY"
	VMStatusFailed       VMStatus = "FAILED"
)

type PersonalVM struct {
	ID            uuid.UUID
	WorkspaceID   uuid.UUID
	ZoneID        uuid.UUID
	OwnerUserID   uuid.UUID
	Name          string
	Image         string
	CPUCores      int32
	MemoryMB      int64
	DiskGB        int64
	SSHPublicKey  string
	SpecHash      []byte
	Status        VMStatus
	OperationID   uuid.UUID
	ProviderName  string
	ProviderNode  *string
	ProviderVMID  *int64
	IPv4Address   *string
	ErrorCode     *string
	ErrorMessage  *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ProvisionedAt *time.Time
}

type CreatePersonalVM struct {
	WorkspaceID  uuid.UUID
	ZoneID       uuid.UUID
	OwnerUserID  uuid.UUID
	Name         string
	Image        string
	CPUCores     int32
	MemoryMB     int64
	DiskGB       int64
	SSHPublicKey string
}

type HypervisorOutboxRecord struct {
	EventID uuid.UUID
	// ZoneID is the immutable dataplane destination for this VM command.
	ZoneID               uuid.UUID
	JobTopic             string
	Payload              []byte
	ActorUserID          uuid.UUID
	Status               string
	JobVersion           int32
	ResourceID           string
	PayloadSchemaVersion int32
	TraceID              []byte
	IdleSeconds          int32
}

type PersonalVMCreateResult struct {
	VM      *PersonalVM
	Created bool
}
