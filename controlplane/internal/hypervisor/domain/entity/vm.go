package hypervisorEntity

import (
	"time"

	"github.com/google/uuid"
)

type VMStatus string

const (
	VMStatusProvisioning VMStatus = "PROVISIONING"
	VMStatusReady        VMStatus = "READY"
)

type PersonalVM struct {
	ID            uuid.UUID
	WorkspaceID   uuid.UUID
	ZoneID        uuid.UUID
	OwnerUserID   uuid.UUID
	Name          string
	Image         string
	ImageID       *uuid.UUID
	ImageRevision *int64
	ImageSHA256   []byte
	CPUCores      int32
	MemoryMB      int64
	DiskGB        int64
	SSHPublicKey  string
	SpecHash      []byte
	Status        VMStatus
	OperationID   uuid.UUID
	ProviderName  string
	ProviderVMID  *int64
	IPv4Address   *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ProvisionedAt *time.Time
}

type CreatePersonalVM struct {
	WorkspaceID  uuid.UUID
	ZoneID       uuid.UUID
	OwnerUserID  uuid.UUID
	Name         string
	ImageID      uuid.UUID
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
	PayloadKeyID         uuid.UUID
	ActorUserID          *uuid.UUID
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
