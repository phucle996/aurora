package hypervisorEntity

import (
	"time"

	"github.com/google/uuid"
)

// HypervisorResourcePlanProjection is a Cost-owned plan revision replicated
// locally solely for the final VM-create CTE. It is flat, immutable and does
// not turn the Hypervisor module into a catalog authority.
type HypervisorResourcePlanProjection struct {
	PlanID         uuid.UUID
	RevisionID     uuid.UUID
	RevisionNumber int64
	Code           string
	DisplayName    string
	Description    string
	BillingModel   string
	CPUCores       int32
	MemoryMIB      int64
	BootDiskGIB    int64
	ContentSHA256  []byte
	EffectiveFrom  time.Time
	EffectiveTo    *time.Time
	State          string
	AllowCreate    bool
	SourceEventID  uuid.UUID
}

type HypervisorResourcePlanProjectionCommand struct {
	EventID        uuid.UUID
	PlanID         uuid.UUID
	RevisionID     uuid.UUID
	RevisionNumber int64
	Code           string
	DisplayName    string
	Description    string
	BillingModel   string
	CPUCores       int32
	MemoryMIB      int64
	BootDiskGIB    int64
	ContentSHA256  []byte
	EffectiveFrom  time.Time
	EffectiveTo    *time.Time
	State          string
	AllowedCreate  bool
}
