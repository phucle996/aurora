package hypervisorEntity

import (
	"time"

	"github.com/google/uuid"
)

type VMStatus string

const (
	VMStatusProvisioning VMStatus = "PROVISIONING"
	VMStatusReady        VMStatus = "READY"
	VMStatusDeleting     VMStatus = "DELETING"
)

type PersonalVM struct {
	ID                    uuid.UUID
	WorkspaceID           uuid.UUID
	ZoneID                uuid.UUID
	OwnerUserID           uuid.UUID
	Name                  string
	Image                 string
	ImageID               *uuid.UUID
	ImageRevision         *int64
	ImageSHA256           []byte
	ResourceProfileCode   string
	CPUCores              int32
	MemoryMB              int64
	BootDiskGB            int64
	DiskGB                int64
	AdditionalDiskSizesGB []int64
	SSHPublicKey          string
	SpecHash              []byte
	Status                VMStatus
	OperationID           uuid.UUID
	ProviderName          string
	ProviderVMID          *int64
	IPv4Address           *string
	CreatedAt             time.Time
	UpdatedAt             time.Time
	ProvisionedAt         *time.Time
}

type CreatePersonalVM struct {
	WorkspaceID         uuid.UUID
	ZoneID              uuid.UUID
	OwnerUserID         uuid.UUID
	Name                string
	ImageID             uuid.UUID
	ResourceProfileCode string
	AdditionalDisks     []PersonalVMCreateAdditionalDisk
	SSHPublicKey        string
}

type PersonalVMCreateAdditionalDisk struct {
	DiskIndex int32
	SizeGB    int64
}

type HypervisorOutboxRecord struct {
	EventID uuid.UUID
	// ZoneID is the immutable dataplane destination for this VM command.
	ZoneID               uuid.UUID
	JobTopic             string
	Payload              []byte
	PayloadKeyID         uuid.UUID
	ActorUserID          *uuid.UUID
	OwnerID              uuid.UUID
	OwnerType            string
	Status               string
	JobVersion           int32
	ResourceID           string
	ResourceName         string
	PayloadSchemaVersion int32
	TraceID              []byte
	IdleSeconds          int32
}



type PersonalVMDeleteResult struct {
	VMID        uuid.UUID
	OperationID uuid.UUID
	Status      VMStatus
}

type PersonalVMCreateResult struct {
	VM      *PersonalVM
	Created bool
}
